package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testKeyPair(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(k)
	block := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
	return k, base64.StdEncoding.EncodeToString(block)
}

func TestParseAppKeyRoundTrips(t *testing.T) {
	want, encoded := testKeyPair(t)

	got, err := parseAppKey(encoded)
	if err != nil {
		t.Fatalf("parseAppKey: %v", err)
	}
	if !got.Equal(want) {
		t.Error("the parsed key is not the key that was encoded")
	}
}

// The two ways this is got wrong, each with its own message.
//
// Both were expensive to diagnose elsewhere in this estate: a raw PEM pasted
// where base64 was wanted, and base64 of the wrong file entirely. Valid
// encoding is not evidence of valid content - base64 of anything decodes
// cleanly - so the second case has to be caught separately from the first.
func TestParseAppKeyRejectsTheTwoLikelyMistakes(t *testing.T) {
	_, encoded := testKeyPair(t)
	rawPEM, _ := base64.StdEncoding.DecodeString(encoded)

	cases := []struct {
		name, input, wants string
	}{
		{"the raw PEM, not base64 of it", string(rawPEM), "not base64"},
		{"base64 of something else", base64.StdEncoding.EncodeToString([]byte("hello")), "not a PEM block"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseAppKey(c.input)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), c.wants) {
				t.Errorf("error should name the mistake %q, got: %v", c.wants, err)
			}
		})
	}
}

// The assertion has to be one GitHub will actually accept.
//
// Verified by checking the signature rather than by matching the shape, and by
// asserting the two details that fail with an unhelpful "bad credentials":
// base64url without padding, and an iat in the past.
func TestAppJWTIsAcceptableToGitHub(t *testing.T) {
	key, _ := testKeyPair(t)
	now := time.Now()

	token, err := appJWT(key, "1234567", now)
	if err != nil {
		t.Fatalf("appJWT: %v", err)
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("want three dot-separated parts, got %d", len(parts))
	}
	if strings.ContainsAny(token, "=+/") {
		t.Error("the token uses standard base64; GitHub requires base64url without padding")
	}

	var header struct{ Alg, Typ string }
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("header is not base64url: %v", err)
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		t.Fatalf("header is not JSON: %v", err)
	}
	if header.Alg != "RS256" {
		t.Errorf("alg is %q, want RS256", header.Alg)
	}

	var claims struct {
		Iat, Exp int64
		Iss      string
	}
	raw, _ = base64.RawURLEncoding.DecodeString(parts[1])
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("claims are not JSON: %v", err)
	}
	if claims.Iss != "1234567" {
		t.Errorf("iss is %q, want the app id", claims.Iss)
	}
	if claims.Iat >= now.Unix() {
		t.Error("iat is not backdated; GitHub refuses a JWT issued in its own future")
	}
	if claims.Exp-claims.Iat > 600 {
		t.Errorf("the token lives %ds, over GitHub's ten-minute ceiling", claims.Exp-claims.Iat)
	}

	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("signature is not base64url: %v", err)
	}
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
		t.Errorf("the signature does not verify: %v", err)
	}
}

// The permissions come from the API, not from a settings page.
//
// A screenshot of intended permissions and the permissions a token actually
// carries are different facts, and only the second decides what this program
// can do.
func TestExchangeReturnsTheTokenAndItsRealPermissions(t *testing.T) {
	key, _ := testKeyPair(t)
	var sawInstallationPath bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("no bearer on %s", r.URL.Path)
		}
		switch {
		case r.URL.Path == "/repos/fensberg/homelab/installation":
			sawInstallationPath = true
			_, _ = w.Write([]byte(`{"id":42}`))
		case r.URL.Path == "/app/installations/42/access_tokens":
			_, _ = w.Write([]byte(`{"token":"ghs_fake","permissions":{"issues":"write","pull_requests":"write","contents":"read"}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	g, perms, err := exchange(srv.URL, "fensberg/homelab", "1234567", key, srv.Client(), time.Now())
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if !sawInstallationPath {
		t.Error("the installation id was not looked up")
	}
	if g.token != "ghs_fake" {
		t.Errorf("token is %q", g.token)
	}
	if perms["pull_requests"] != "write" || perms["contents"] != "read" {
		t.Errorf("permissions not reported back: %v", perms)
	}
}

// A token issued with no token, and no error, must not be treated as success.
func TestExchangeRefusesAnEmptyToken(t *testing.T) {
	key, _ := testKeyPair(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/installation") {
			_, _ = w.Write([]byte(`{"id":42}`))
			return
		}
		_, _ = w.Write([]byte(`{"permissions":{}}`))
	}))
	defer srv.Close()

	if _, _, err := exchange(srv.URL, "o/r", "1", key, srv.Client(), time.Now()); err == nil {
		t.Fatal("an empty token was accepted as success")
	}
}

// The whole point of the clerk, asserted at the wire.
func TestSayAlwaysSendsCommentAndNeverApproves(t *testing.T) {
	var sent map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/pulls/7/reviews") {
			t.Errorf("posted to %s, not the reviews endpoint", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&sent)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	g := &gh{api: srv.URL, repo: "o/r", token: "ghs_fake", http: srv.Client()}
	if err := g.say(7, "two things I would do differently"); err != nil {
		t.Fatalf("say: %v", err)
	}
	if sent["event"] != "COMMENT" {
		t.Errorf("event is %q; the clerk may only ever comment", sent["event"])
	}
	if sent["body"] == "" {
		t.Error("the body was not sent")
	}
}

// Guards a call site that does not exist yet.
//
// The test above proves what say() sends today. This one exists so that adding
// a second review call - a "looks fine, approve it" someone thinks is
// harmless - fails the build rather than quietly acquiring the lever this
// whole design removes.
//
// It parses for string *literals* rather than grepping the file. The first
// version grepped, and failed on this package's own comments explaining why
// APPROVE is dangerous - which would have forced the explanation out of the
// source to keep the build green. A guard that makes you delete the reasoning
// is worse than no guard. Only a literal can reach the API, so only a literal
// is what this looks at.
func TestNoStringLiteralInThisPackageCanApprove(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no sources found, so this test proves nothing (%v)", err)
	}

	var checked int
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		tree, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", f, err)
		}
		checked++
		ast.Inspect(tree, func(n ast.Node) bool {
			lit, isLit := n.(*ast.BasicLit)
			if !isLit || lit.Kind != token.STRING {
				return true
			}
			value := strings.Trim(lit.Value, "`\"")
			if value == "APPROVE" || value == "REQUEST_CHANGES" {
				t.Errorf("%s has the literal %q. The clerk is an outside party and may not "+
					"approve or block: an App's approval counts toward branch protection.", f, value)
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no non-test sources parsed, so this test proves nothing")
	}
}

// The installation token lives an hour, and this output is world-readable.
func TestGitHubErrorsDoNotQuoteTheToken(t *testing.T) {
	const tok = "ghs_secrettokenvalue"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad credentials for "+tok, http.StatusUnauthorized)
	}))
	defer srv.Close()

	g := &gh{api: srv.URL, repo: "o/r", token: tok, http: srv.Client()}
	err := g.say(7, "body")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), tok) {
		t.Errorf("the error quotes the installation token: %v", err)
	}
}
