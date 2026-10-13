package ghapi_test

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"crypto"

	. "homelab/details/ghapi"
	"homelab/details/ghapi/ghapitest"
)

func aKey(t *testing.T) (*rsa.PrivateKey, []byte, []byte) {
	t.Helper()
	key, issued := ghapitest.Key(t)
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return key, issued, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
}

// The sign-in is asked of the installation it is for, carrying an assertion
// the App's own key signed, issued in the past and good for under ten
// minutes; and what comes back is the token.
func TestAnAppSignsInAsItsInstallationWithAnAssertionItsKeySigned(t *testing.T) {
	key, pkcs1, pkcs8 := aKey(t)
	now := time.Unix(1_800_000_000, 0)

	for name, pemKey := range map[string][]byte{"as GitHub issues it": pkcs1, "as openssl converts it": pkcs8} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/app/installations/77/access_tokens" {
				t.Errorf("%s: asked %s %s", name, r.Method, r.URL.Path)
			}
			parts := strings.Split(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), ".")
			if len(parts) != 3 {
				t.Fatalf("%s: the assertion has %d parts", name, len(parts))
			}
			digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
			sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
			if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
				t.Errorf("%s: the assertion was not signed by the App's key: %v", name, err)
			}
			var claims struct {
				Iat, Exp int64
				Iss      string
			}
			raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
			_ = json.Unmarshal(raw, &claims)
			if claims.Iss != "42" || claims.Iat >= now.Unix() || claims.Exp-claims.Iat > 600 || claims.Exp <= now.Unix() {
				t.Errorf("%s: the assertion claims %+v", name, claims)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"handed-over"}`))
		}))
		token, err := App{Base: srv.URL, ID: "42", InstallationID: "77", Key: pemKey}.Token(now)
		srv.Close()
		if err != nil || token != "handed-over" {
			t.Errorf("%s: got %q, %v", name, token, err)
		}
	}
}

// A sign-in that cannot be made says why, and a key's contents never reach
// what it says.
func TestASignInThatFailsSaysWhyAndNeverQuotesTheKey(t *testing.T) {
	_, pkcs1, _ := aKey(t)
	refuses := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer refuses.Close()
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer empty.Close()

	for name, c := range map[string]struct {
		app  App
		says string
	}{
		"a key that is not PEM":     {App{Base: refuses.URL, Key: []byte("not-a-key-at-all")}, "not PEM-encoded"},
		"PEM that is not a key":     {App{Base: refuses.URL, Key: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("nonsense")})}, "not an RSA key"},
		"a refusal":                 {App{Base: refuses.URL, Key: pkcs1}, "GitHub refused (401): Bad credentials"},
		"an answer without a token": {App{Base: empty.URL, Key: pkcs1}, "without a token"},
	} {
		_, err := c.app.Token(time.Now())
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: got %v, want it to say %q", name, err, c.says)
		}
		if err != nil && (strings.Contains(err.Error(), "not-a-key-at-all") || strings.Contains(err.Error(), "nonsense")) {
			t.Errorf("%s: the key's contents reached the message", name)
		}
	}
}

// A question is asked with the token, and a refusal or an answer of the
// wrong shape is an error and never an empty result.
func TestAQuestionIsAskedWithTheTokenAndARefusalIsNeverAnEmptyAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer a-token" {
			t.Errorf("the question did not carry the token")
		}
		switch r.URL.Path {
		case "/things":
			_, _ = w.Write([]byte(`[{"name":"one"},{"name":"two"}]`))
		case "/shapeless":
			_, _ = w.Write([]byte(`{"name":"not a list"}`))
		default:
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`not json`))
		}
	}))
	defer srv.Close()

	type thing struct {
		Name string `json:"name"`
	}
	got, err := Get[[]thing](srv.URL, "a-token", "/things")
	if err != nil || len(got) != 2 || got[1].Name != "two" {
		t.Errorf("got %v, %v", got, err)
	}
	if _, err := Get[[]thing](srv.URL, "a-token", "/shapeless"); err == nil {
		t.Error("an answer of the wrong shape was read as an answer")
	}
	if got, err := Get[[]thing](srv.URL, "a-token", "/forbidden"); err == nil || got != nil || !strings.Contains(err.Error(), "(403): no reason given") {
		t.Errorf("a refusal was read as %v, %v", got, err)
	}
}

// A credential as the vault holds it becomes the installation it signs in
// as, and a key that was not encoded is refused without being quoted.
func TestACredentialBecomesItsInstallationAndAnUnencodedKeyIsRefused(t *testing.T) {
	c := AppCredential{AppID: "42", InstallationID: "77", PrivateKey: " " + base64.StdEncoding.EncodeToString([]byte("the key")) + "\n"}
	app, err := c.App("https://example.test")
	if err != nil || app.ID != "42" || app.InstallationID != "77" || string(app.Key) != "the key" || app.Base != "https://example.test" {
		t.Errorf("got %+v, %v", app, err)
	}
	c.PrivateKey = "-----BEGIN pasted as it came-----"
	if _, err := c.App(""); err == nil || strings.Contains(err.Error(), "pasted as it came") {
		t.Errorf("an unencoded key: %v", err)
	}
}
