package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"homelab/details/onepassword/standin"
	"homelab/details/vaults"
)

// The notary runs the 1Password CLI as a program, so its tests give it one.
func TestMain(m *testing.M) {
	if status, was := standin.Answer(); was {
		os.Exit(status)
	}
	os.Exit(m.Run())
}

// world is everything the notary reaches, stood in for: a vault, the
// domain's records, and an issuer that looks for a record before it accepts
// a name.
type world struct {
	t *testing.T
	standin.Vaults
	mu sync.Mutex
	// The domain's records, by id.
	records map[string]map[string]any
	nextID  int
	// What the issuer was asked and did.
	accounts, orders int
	life             time.Duration
	issuedFor        [][]string
	profiles         []string
	ca               *x509.Certificate
	caKey            *ecdsa.PrivateKey
}

const (
	domain = "served.example"
	site   = "site9"
)

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, records: map[string]map[string]any{}, life: 45 * 24 * time.Hour}
	w.Vaults = standin.New(t, vaults.Estate, vaults.EstateShared, vaults.SiteShared(site))
	w.Keep(recordsToken.Vault, recordsToken.Item, recordsToken.Field, "a-token")
	w.Keep(servedDomain.Vault, servedDomain.Item, servedDomain.Field, domain)

	w.caKey, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	authority := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "stand-in issuer"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, authority, authority, &w.caKey.PublicKey, w.caKey)
	if err != nil {
		t.Fatal(err)
	}
	w.ca, _ = x509.ParseCertificate(der)

	srv := httptest.NewServer(http.HandlerFunc(w.serve))
	t.Cleanup(srv.Close)
	wasAPI, wasDirectory, wasRehearsal := apiBase, directory, rehearsal
	apiBase = srv.URL + "/dns"
	directory = srv.URL + "/real/directory"
	rehearsal = srv.URL + "/rehearsal/directory"
	t.Cleanup(func() { apiBase, directory, rehearsal = wasAPI, wasDirectory, wasRehearsal })
	wasSettle := settle
	settle = 0
	t.Cleanup(func() { settle = wasSettle })
	return w
}

// recordsOf is the domain's records of one type, as "name value".
func (w *world) recordsOf(kind string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, r := range w.records {
		if r["type"] == kind {
			value, _ := r["content"].(string)
			if data, ok := r["data"].(map[string]any); ok {
				value = fmt.Sprint(data["tag"], " ", data["value"])
			}
			out = append(out, fmt.Sprint(r["name"], " ", value))
		}
	}
	sort.Strings(out)
	return out
}

func (w *world) serve(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()
	base := "http://" + r.Host
	answer := func(status int, v any) {
		rw.Header().Set("Replay-Nonce", fmt.Sprint("nonce-", time.Now().UnixNano()))
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(status)
		_ = json.NewEncoder(rw).Encode(v)
	}
	// What a signed request carries, with the signature taken on trust: the
	// library's signing is the library's to test.
	payload := func() map[string]any {
		var signed struct {
			Payload string `json:"payload"`
		}
		_ = json.NewDecoder(r.Body).Decode(&signed)
		raw, _ := base64.RawURLEncoding.DecodeString(signed.Payload)
		out := map[string]any{}
		_ = json.Unmarshal(raw, &out)
		return out
	}

	path := r.URL.Path
	if strings.HasPrefix(path, "/dns") {
		w.dns(rw, r, strings.TrimPrefix(path, "/dns"))
		return
	}
	service := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)[0]
	at := base + "/" + service
	order := func(status string) map[string]any {
		names := w.issuedFor[len(w.issuedFor)-1]
		out := map[string]any{"status": status, "finalize": at + "/finalize", "certificate": at + "/certificate"}
		var ids []map[string]string
		var authz []string
		for _, n := range names {
			ids = append(ids, map[string]string{"type": "dns", "value": n})
			authz = append(authz, at+"/authz/"+n)
		}
		out["identifiers"], out["authorizations"] = ids, authz
		return out
	}
	proved := func(name string) bool {
		for _, rec := range w.records {
			if rec["type"] == "TXT" && rec["name"] == "_acme-challenge."+name {
				return true
			}
		}
		return false
	}
	switch rest := strings.TrimPrefix(path, "/"+service); {
	case rest == "/directory":
		answer(200, map[string]any{"newNonce": at + "/nonce", "newAccount": at + "/account", "newOrder": at + "/order",
			"meta": map[string]any{"profiles": map[string]string{profile: "the short one", "classic": "the long one"}}})
	case rest == "/nonce":
		answer(200, nil)
	case rest == "/account":
		w.accounts++
		rw.Header().Set("Location", at+"/account/1")
		answer(201, map[string]any{"status": "valid"})
	case rest == "/order":
		asked := payload()
		var names []string
		for _, id := range asked["identifiers"].([]any) {
			names = append(names, id.(map[string]any)["value"].(string))
		}
		w.orders++
		w.issuedFor = append(w.issuedFor, names)
		w.profiles = append(w.profiles, fmt.Sprint(asked["profile"]))
		rw.Header().Set("Location", at+"/order/1")
		answer(201, order("pending"))
	case rest == "/order/1":
		answer(200, order("ready"))
	case strings.HasPrefix(rest, "/authz/"):
		name := strings.TrimPrefix(rest, "/authz/")
		status := "pending"
		if proved(name) {
			status = "valid"
		}
		answer(200, map[string]any{"status": status, "identifier": map[string]string{"type": "dns", "value": name},
			"challenges": []map[string]string{{"type": "http-01", "url": at + "/challenge/other", "token": "other"}, {"type": "dns-01", "url": at + "/challenge/" + name, "token": "token-" + name, "status": status}}})
	case strings.HasPrefix(rest, "/challenge/"):
		name := strings.TrimPrefix(rest, "/challenge/")
		if !proved(name) {
			answer(400, map[string]any{"type": "urn:ietf:params:acme:error:unauthorized", "detail": "no record proves the name"})
			return
		}
		answer(200, map[string]string{"type": "dns-01", "url": at + rest, "token": "token-" + name, "status": "valid"})
	case rest == "/finalize":
		raw, _ := base64.RawURLEncoding.DecodeString(fmt.Sprint(payload()["csr"]))
		request, err := x509.ParseCertificateRequest(raw)
		if err != nil {
			answer(400, map[string]any{"detail": "not a request"})
			return
		}
		leaf := &x509.Certificate{SerialNumber: big.NewInt(int64(w.orders) + 1), DNSNames: request.DNSNames, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(w.life - time.Hour)}
		der, _ := x509.CreateCertificate(rand.Reader, leaf, w.ca, request.PublicKey, w.caKey)
		w.records["issued"] = map[string]any{"type": "issued", "content": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) + string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: w.ca.Raw}))}
		answer(200, order("valid"))
	case rest == "/certificate":
		rw.Header().Set("Replay-Nonce", "nonce-certificate")
		rw.Header().Set("Content-Type", "application/pem-certificate-chain")
		_, _ = io.WriteString(rw, w.records["issued"]["content"].(string))
	default:
		answer(404, map[string]any{"detail": "the stand-in issuer does not answer " + path})
	}
}

// dns is the domain's records as the vendor's API serves them.
func (w *world) dns(rw http.ResponseWriter, r *http.Request, path string) {
	answer := func(v any) {
		_ = json.NewEncoder(rw).Encode(map[string]any{"success": true, "result": v})
	}
	switch {
	case path == "/zones":
		if r.URL.Query().Get("name") != domain || r.Header.Get("Authorization") != "Bearer a-token" {
			answer([]any{})
			return
		}
		answer([]map[string]string{{"id": "zone1"}})
	case path == "/zones/zone1/dns_records" && r.Method == http.MethodGet:
		out := []map[string]any{}
		for _, rec := range w.records {
			if rec["type"] == r.URL.Query().Get("type") && rec["name"] == r.URL.Query().Get("name") {
				out = append(out, rec)
			}
		}
		answer(out)
	case path == "/zones/zone1/dns_records" && r.Method == http.MethodPost:
		rec := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&rec)
		w.nextID++
		rec["id"] = fmt.Sprint("r", w.nextID)
		w.records[rec["id"].(string)] = rec
		answer(rec)
	case strings.HasPrefix(path, "/zones/zone1/dns_records/") && r.Method == http.MethodDelete:
		delete(w.records, strings.TrimPrefix(path, "/zones/zone1/dns_records/"))
		answer(map[string]string{})
	default:
		rw.WriteHeader(404)
		_ = json.NewEncoder(rw).Encode(map[string]any{"success": false, "errors": []map[string]any{{"code": 404, "message": "no such thing"}}})
	}
}

// certificateFor is a certificate as a vault keeps one, for these names and
// with this long left, for the tests of when one is due.
func certificateFor(t *testing.T, left time.Duration, names ...string) string {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	c := &x509.Certificate{SerialNumber: big.NewInt(7), DNSNames: names, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(left)}
	der, err := x509.CreateCertificate(rand.Reader, c, c, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestASiteIsIssuedACertificateForExactlyTheNamesItWasGranted(t *testing.T) {
	w := newWorld(t)
	w.Keep(vaults.SiteShared(site), servedItem, namesField, "front-door."+domain+" side-door."+domain)

	if err := issue(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	leaf, err := leafOf(w.Kept(vaults.SiteShared(site), certificateItem, chainField))
	if err != nil {
		t.Fatalf("the site's vault does not hold a certificate that can be read: %v", err)
	}
	if got := strings.Join(leaf.DNSNames, " "); got != "front-door."+domain+" side-door."+domain {
		t.Errorf("the certificate is for %q", got)
	}
	if got := w.Kept(vaults.SiteShared(site), certificateItem, expiresField); got != leaf.NotAfter.UTC().Format(time.RFC3339) {
		t.Errorf("the vault says the certificate runs out at %q and it runs out at %s", got, leaf.NotAfter.UTC().Format(time.RFC3339))
	}
	// The key in the vault is the certificate's own, or the site serves
	// nothing with the pair.
	key, err := keyFrom(w.Kept(vaults.SiteShared(site), certificateItem, keyField))
	if err != nil {
		t.Fatal(err)
	}
	if !key.PublicKey.Equal(leaf.PublicKey) {
		t.Error("the key granted is not the key the certificate is for")
	}
	if fmt.Sprint(w.profiles) != "["+profile+"]" {
		t.Errorf("the issuer was asked for the kinds %v, and the short one is %q", w.profiles, profile)
	}
	if left := w.recordsOf("TXT"); len(left) != 0 {
		t.Errorf("records that proved a name were left on the domain: %d", len(left))
	}
	if bound := w.recordsOf("CAA"); len(bound) != 1 || !strings.HasPrefix(bound[0], domain+" issue "+issuerName+"; accounturi=http") || !strings.HasSuffix(bound[0], "/real/account/1") {
		t.Errorf("the domain does not say that the operator's account alone may be issued to: %v", bound)
	}
	if w.Kept(vaults.Estate, accountKey.Item, accountKey.Field) == "" {
		t.Error("the key the issuer knows the operator by was not kept in the estate vault")
	}

	// And a second run finds it current, and asks nothing of the issuer.
	orders := w.orders
	if err := issue(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if w.orders != orders {
		t.Error("a site whose certificate is current was issued another")
	}
}

func TestARehearsalWritesNothingAndBindsNothing(t *testing.T) {
	w := newWorld(t)
	w.Keep(vaults.SiteShared(site), servedItem, namesField, "front-door."+domain)

	if err := issue(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if w.orders != 1 {
		t.Fatalf("a rehearsal made %d order(s) of the issuer", w.orders)
	}
	if w.Kept(vaults.SiteShared(site), certificateItem, chainField) != "" || w.Kept(vaults.Estate, accountKey.Item, accountKey.Field) != "" {
		t.Error("a rehearsal wrote to a vault")
	}
	if len(w.recordsOf("CAA")) != 0 || len(w.recordsOf("TXT")) != 0 {
		t.Error("a rehearsal left a record on the domain")
	}
}

func TestACertificateGoodForLongerThanTheOperatorChoseIsNotGranted(t *testing.T) {
	w := newWorld(t)
	w.life = 90 * 24 * time.Hour
	w.Keep(vaults.SiteShared(site), servedItem, namesField, "front-door."+domain)

	err := issue(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "good for 90 days where at most 46") {
		t.Fatalf("a ninety day certificate was not refused for its length: %v", err)
	}
	if w.Kept(vaults.SiteShared(site), certificateItem, chainField) != "" {
		t.Error("the certificate was granted all the same")
	}
	if len(w.recordsOf("TXT")) != 0 {
		t.Error("a refusal left a record that proves a name on the domain")
	}
}

func TestASiteGrantedNoNamesIsIssuedNothing(t *testing.T) {
	w := newWorld(t)
	if err := issue(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if w.accounts != 0 || w.orders != 0 {
		t.Error("the issuer was asked on behalf of a site that serves nothing")
	}
}

func TestOtherRecordsThatPermitAnIssuerAreCountedAndLeftAlone(t *testing.T) {
	w := newWorld(t)
	other := func(tag string) map[string]any {
		return map[string]any{"type": "CAA", "name": domain, "data": map[string]any{"tag": tag, "value": "another.example"}}
	}
	w.records["a"], w.records["b"], w.records["c"] = other("issue"), other("issuewild"), other("iodef")
	z, err := zoneOf("a-token", domain)
	if err != nil {
		t.Fatal(err)
	}
	for run := 1; run <= 2; run++ {
		others, err := z.bind(domain, "https://issuer.example/account/1")
		if err != nil || others != 2 {
			t.Fatalf("run %d: counted %d other record(s) that permit an issuer, %v; there are two", run, others, err)
		}
		if got := len(w.recordsOf("CAA")); got != 4 {
			t.Fatalf("run %d: the domain holds %d such record(s); the three it had and the operator's one are four", run, got)
		}
	}
	if _, err := zoneOf("another", domain); err == nil || !strings.Contains(err.Error(), "finds 0 zone(s)") {
		t.Errorf("a token that cannot see the domain was not told so: %v", err)
	}
}

func TestDueSaysWhyASiteIsOwedACertificate(t *testing.T) {
	now := time.Now()
	day := 24 * time.Hour
	for name, c := range map[string]struct {
		held string
		want []string
		why  string
		err  string
	}{
		"none held":             {"", []string{"a.example"}, "it holds none", ""},
		"current":               {certificateFor(t, 30*day, "a.example", "b.example"), []string{"b.example", "a.example"}, "", ""},
		"just long enough left": {certificateFor(t, 20*day+time.Hour, "a.example"), []string{"a.example"}, "", ""},
		"too little left":       {certificateFor(t, 19*day+time.Hour, "a.example"), []string{"a.example"}, "has 19 day(s) left", ""},
		"run out":               {certificateFor(t, -day, "a.example"), []string{"a.example"}, "has 0 day(s) left", ""},
		"a name added":          {certificateFor(t, 30*day, "a.example"), []string{"a.example", "b.example"}, "for other names", ""},
		"a name taken away":     {certificateFor(t, 30*day, "a.example", "b.example"), []string{"a.example"}, "for other names", ""},
		"not a certificate":     {"bm90IGEgY2VydGlmaWNhdGU=", []string{"a.example"}, "", "holds no certificate"},
		"not base64":            {"-----BEGIN", []string{"a.example"}, "", "not base64"},
		"no names":              {"", nil, "", "the list is empty"},
	} {
		why, err := due(c.held, c.want, now)
		switch {
		case c.err != "":
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: want an error saying %q, got %q, %v", name, c.err, why, err)
			}
		case err != nil:
			t.Errorf("%s: %v", name, err)
		case c.why == "" && why != "", c.why != "" && !strings.Contains(why, c.why):
			t.Errorf("%s: want %q, got %q", name, c.why, why)
		}
	}
}

func TestTheProgramEndsWithAStatusThatSaysWhatHappened(t *testing.T) {
	w := newWorld(t)
	for _, wrong := range [][]string{nil, {"renew"}, {"issue", "-everything"}, {"issue", "now"}} {
		if got := run(wrong); got != 2 {
			t.Errorf("%v is not something the notary is asked, and it ended with %d", wrong, got)
		}
	}
	if w.accounts != 0 {
		t.Fatal("something not asked properly reached the issuer")
	}

	// Started as a program, with a site that is owed a certificate.
	w.Keep(vaults.SiteShared(site), servedItem, namesField, "front-door."+domain)
	was := os.Args
	os.Args = []string{"notary", "issue", "-rehearse"}
	t.Cleanup(func() { os.Args = was })
	main()
	if w.orders != 1 || w.Kept(vaults.SiteShared(site), certificateItem, chainField) != "" {
		t.Errorf("a rehearsal started as a program made %d order(s) and wrote to the vault or did not", w.orders)
	}

	// And one that cannot be done says so by its status.
	w.life = 90 * 24 * time.Hour
	if got := run([]string{"issue"}); got != 1 {
		t.Errorf("a certificate that was refused ended the program with %d", got)
	}
}
