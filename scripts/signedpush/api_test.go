package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func stub(t *testing.T, handler http.HandlerFunc) (*api, *[]map[string]any) {
	t.Helper()
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	// apiBase is a const, so point the client at the stub by overriding the
	// transport rather than the URL.
	client := srv.Client()
	client.Transport = rewrite{srv.URL, client.Transport}
	return &api{token: "test", http: client}, &bodies
}

type rewrite struct {
	base string
	next http.RoundTripper
}

func (rw rewrite) RoundTrip(r *http.Request) (*http.Response, error) {
	u := strings.TrimPrefix(rw.base, "http://")
	r.URL.Scheme, r.URL.Host = "http", u
	return rw.next.RoundTrip(r)
}

// The entire point of this program. If GitHub declines to sign, that must be a
// hard failure - an unverified commit is exactly the outcome being prevented,
// and nothing downstream would notice it.
func TestCreateCommitRejectsAnUnverifiedResult(t *testing.T) {
	a, _ := stub(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sha":          "abc123",
			"verification": map[string]any{"verified": false, "reason": "unsigned"},
		})
	})

	_, err := a.createCommit("o", "r", "msg", "tree", []string{"parent"})
	if err == nil {
		t.Fatal("an unverified commit must be an error, not a success")
	}
	if !strings.Contains(err.Error(), "UNVERIFIED") {
		t.Errorf("the error should say the commit was unverified, got: %v", err)
	}
}

func TestCreateCommitAcceptsAVerifiedResult(t *testing.T) {
	a, _ := stub(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sha":          "abc123",
			"verification": map[string]any{"verified": true, "reason": "valid"},
		})
	})

	sha, err := a.createCommit("o", "r", "msg", "tree", []string{"parent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sha != "abc123" {
		t.Errorf("got sha %q", sha)
	}
}

// GitHub signs an app's commit only when the request supplies no author,
// committer or signature. Sending any of them silently yields an unsigned
// commit, so the request body is asserted rather than assumed.
func TestCreateCommitSendsNoAuthorshipFields(t *testing.T) {
	a, bodies := stub(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sha":          "abc123",
			"verification": map[string]any{"verified": true, "reason": "valid"},
		})
	})

	if _, err := a.createCommit("o", "r", "msg", "tree", []string{"parent"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*bodies) != 1 {
		t.Fatalf("expected one request, got %d", len(*bodies))
	}
	for _, forbidden := range []string{"author", "committer", "signature"} {
		if _, present := (*bodies)[0][forbidden]; present {
			t.Errorf("request carries %q; GitHub will refuse to sign the commit", forbidden)
		}
	}
	for _, required := range []string{"message", "tree", "parents"} {
		if _, present := (*bodies)[0][required]; !present {
			t.Errorf("request is missing %q", required)
		}
	}
}

// A force update is what non_fast_forward refuses. Wanting one means the local
// branch was rewritten, which is worth stopping for rather than pushing past.
func TestSetRefNeverForces(t *testing.T) {
	a, bodies := stub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})

	if err := a.setRef("o", "r", "heads/b", "sha", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if force, ok := (*bodies)[0]["force"]; ok && force != false {
		t.Errorf("force = %v, want false or absent", force)
	}
}
