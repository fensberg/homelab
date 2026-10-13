package ghapitest

import (
	"net/http"
	"testing"
	"time"

	"homelab/details/ghapi"
)

// The stand-in hands a token to the credential it returns, and a question
// asked with that token reaches the answer given.
func TestTheStandInSignsItsOwnCredentialInAndPassesItsQuestionsOn(t *testing.T) {
	base, credential := Server(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`["` + r.URL.Path + `"]`))
	})
	app, err := credential.App(base)
	if err != nil {
		t.Fatal(err)
	}
	token, err := app.Token(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ghapi.Get[[]string](base, token, "/asked"); err != nil || len(got) != 1 || got[0] != "/asked" {
		t.Errorf("got %v, %v", got, err)
	}
}
