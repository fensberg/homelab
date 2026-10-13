// Package ghapitest stands in for GitHub in another program's tests: a local
// server an App can sign in to, and a credential that App holds. Written
// once so no program's tests restate how the sign-in is asked and answered.
package ghapitest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"homelab/details/ghapi"
)

// Key makes a key and returns it with its PEM as GitHub issues an App's.
func Key(t testing.TB) (*rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

// Server starts a stand-in that hands a token to the credential it returns,
// and passes every question asked with that token to answer. A question
// asked any other way fails the test.
func Server(t testing.TB, answer http.HandlerFunc) (base string, credential ghapi.AppCredential) {
	t.Helper()
	_, issued := Key(t)
	credential = ghapi.AppCredential{
		AppID:          "42",
		InstallationID: "77",
		PrivateKey:     base64.StdEncoding.EncodeToString(issued),
	}
	const token = "handed-by-the-stand-in"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/"+credential.InstallationID+"/access_tokens":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"` + token + `"}`))
		case r.Header.Get("Authorization") == "Bearer "+token:
			answer(w, r)
		default:
			t.Errorf("the stand-in was asked %s %s without the token it handed over", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, credential
}
