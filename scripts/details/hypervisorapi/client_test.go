package hypervisorapi

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// anAuthority is an authority like the one a hypervisor makes for itself, and
// a way to have it sign a certificate for a node.
type anAuthority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	der  []byte
}

func newAuthority(t *testing.T) anAuthority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "a hypervisor's own authority"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return anAuthority{cert, key, der}
}

// encoded is the authority as the vault keeps it.
func (a anAuthority) encoded() string {
	return base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: a.der}))
}

// serving starts an API that presents a certificate this authority signed for
// the named node - by its name, as a hypervisor's certificate does, and not by
// the address it happens to be reached at.
func (a anAuthority) serving(t *testing.T, node string) *httptest.Server {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: node},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{node}, IPAddresses: []net.IP{net.ParseIP("192.0.2.10")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, &key.PublicKey, a.key)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("the api answered"))
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS13}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, authority, node, url string) error {
	t.Helper()
	client, err := Client(authority, node, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get(url)
	if err == nil {
		resp.Body.Close()
	}
	return err
}

// The node is reached at an address its certificate does not list, which is
// the ordinary case: it is known by its name.
func TestANodeIsBelievedOnItsAuthoritysWordAtWhateverAddress(t *testing.T) {
	a := newAuthority(t)
	srv := a.serving(t, "a-node")
	if err := get(t, a.encoded(), "a-node", srv.URL); err != nil {
		t.Fatalf("the node's own certificate, signed by its own authority, was refused: %v", err)
	}
}

// What verification being off allowed: anything answering at the address.
func TestSomethingElseAnsweringAtTheAddressIsRefused(t *testing.T) {
	theirs := newAuthority(t)
	srv := theirs.serving(t, "a-node")
	ours := newAuthority(t)
	if err := get(t, ours.encoded(), "a-node", srv.URL); err == nil {
		t.Fatal("a certificate signed by another authority was believed, so anything between here and the host could answer as it")
	}
}

func TestAnotherNodeOfTheSameAuthorityIsNotThisOne(t *testing.T) {
	a := newAuthority(t)
	srv := a.serving(t, "another-node")
	if err := get(t, a.encoded(), "a-node", srv.URL); err == nil {
		t.Fatal("a certificate for another node was accepted as this one's")
	}
}

// The system's roots are not consulted: a certificate a public authority
// would vouch for is still not this hypervisor's.
func TestAPubliclyTrustedCertificateIsNotTheHypervisors(t *testing.T) {
	a := newAuthority(t)
	client, err := Client(a.encoded(), "a-node", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	pool := client.Transport.(*http.Transport).TLSClientConfig.RootCAs
	if pool == nil {
		t.Fatal("the client falls back to the system's roots")
	}
	if !pool.Equal(func() *x509.CertPool { p := x509.NewCertPool(); p.AddCert(a.cert); return p }()) {
		t.Error("the client trusts something besides the authority it was given")
	}
}

func TestAnAuthorityThatIsNotOneIsRefusedBeforeAnythingIsSent(t *testing.T) {
	for name, authority := range map[string]string{
		"nothing":             "",
		"not base64":          "-----BEGIN CERTIFICATE-----",
		"base64 of something": base64.StdEncoding.EncodeToString([]byte("a-value")),
	} {
		if _, err := Client(authority, "a-node", time.Second); err == nil {
			t.Errorf("%s was taken for an authority", name)
		}
	}
	a := newAuthority(t)
	if _, err := Client(a.encoded(), " ", time.Second); err == nil || !strings.Contains(err.Error(), "node") {
		t.Errorf("a client was made for no node: %v", err)
	}
}

func TestTheAuthorityIsKeptInTheSitesOwnVault(t *testing.T) {
	if got := AuthorityRef("site0"); got != "op://site0/hypervisor/authority" {
		t.Fatalf("looked for at %q", got)
	}
}
