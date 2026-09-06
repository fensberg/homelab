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

// The ordinary path never forces. A rewrite has to be asked for explicitly,
// which is what setRefForce is for.
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

// The force path moves the ref and never deletes it.
//
// Deleting is what closes a pull request - GitHub closes one when its head ref
// disappears and then refuses to reopen it. Four were lost that way in two
// days, each to a deliberate rebase that had no other way to publish.
func TestSetRefForceMovesTheRefRatherThanDeletingIt(t *testing.T) {
	var methods []string
	a, bodies := stub(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"object":{"sha":"oldsha"}}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})

	if err := a.setRefForce("o", "r", "heads/b", "newsha", "oldsha"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, m := range methods {
		if m == http.MethodDelete {
			t.Fatal("the ref was deleted, which is the thing that closes the pull request")
		}
	}
	last := (*bodies)[len(*bodies)-1]
	if last["force"] != true {
		t.Errorf("force = %v, want true - without it GitHub refuses the non-fast-forward", last["force"])
	}
	if last["sha"] != "newsha" {
		t.Errorf("sha = %v, want newsha", last["sha"])
	}
}

// The lease. GitHub has no compare-and-swap, so a concurrent publish would be
// silently discarded by a blind force - and that is exactly the case where
// somebody loses work they had every reason to think was safe.
func TestSetRefForceRefusesWhenTheBranchMovedUnderneath(t *testing.T) {
	a, _ := stub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"object":{"sha":"somebodyelsespush"}}`))
			return
		}
		t.Error("a write was attempted after the ref had moved, which would discard the other publish")
		_, _ = w.Write([]byte(`{}`))
	})

	err := a.setRefForce("o", "r", "heads/b", "newsha", "whatiexpected")
	if err == nil {
		t.Fatal("a moved ref was force-updated anyway")
	}
	if !strings.Contains(err.Error(), "moved while this was publishing") {
		t.Errorf("the refusal does not explain what happened: %v", err)
	}
}
