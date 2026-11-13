package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A published branch is opened into main, as the title, description and
// draft state asked for.
func TestAPullRequestIsOpenedIntoMainAsAsked(t *testing.T) {
	a, bodies := stub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if got := r.URL.Query().Get("head"); got != "o:revert/x" {
				t.Errorf("looked for open pull requests from %q, want o:revert/x", got)
			}
			_, _ = w.Write([]byte("[]"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"html_url": "https://example.test/pull/9"})
	})
	url, opened, err := a.openPullRequest("o", "r", "revert/x", proposal{title: "revert: x", body: "why", draft: true})
	if err != nil || !opened || url != "https://example.test/pull/9" {
		t.Fatalf("got %q, %v, %v; want the new pull request, opened", url, opened, err)
	}
	sent := (*bodies)[1]
	for field, want := range map[string]any{"title": "revert: x", "body": "why", "head": "revert/x", "base": "main", "draft": true} {
		if sent[field] != want {
			t.Errorf("%s was sent as %v, want %v", field, sent[field], want)
		}
	}
}

// A pull request already open from the branch is named, and no second one is
// asked for: a job run again finishes rather than failing.
func TestAPullRequestAlreadyOpenIsNamedNotOpenedAgain(t *testing.T) {
	a, bodies := stub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("%s %s asked for a second pull request", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`[{"html_url": "https://example.test/pull/3"}]`))
	})
	url, opened, err := a.openPullRequest("o", "r", "revert/x", proposal{title: "t", body: "b"})
	if err != nil || opened || url != "https://example.test/pull/3" || len(*bodies) != 1 {
		t.Errorf("got %q, opened %v, %d call(s), %v; want the open one named in one call", url, opened, len(*bodies), err)
	}
}

// What the pull request will say is read before anything is published: found
// missing afterwards, it would leave a branch out that nobody opened.
func TestAProposalIsCheckedBeforeAnythingIsPublished(t *testing.T) {
	dir := t.TempDir()
	full, empty := filepath.Join(dir, "body.md"), filepath.Join(dir, "empty.md")
	if err := os.WriteFile(full, []byte("## What you need to do\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(empty, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name      string
		open      bool
		title     string
		body      string
		draft     bool
		wantError bool
	}{
		{"nothing asked", false, "", "", false, false},
		{"a whole proposal", true, "revert: x", full, true, false},
		{"no title", true, " ", full, false, true},
		{"no description", true, "t", "", false, true},
		{"an empty description", true, "t", empty, false, true},
		{"a description that is not there", true, "t", filepath.Join(dir, "gone.md"), false, true},
		{"a title with nothing to open", false, "t", "", false, true},
		{"a draft with nothing to open", false, "", "", true, true},
	} {
		p, err := proposalFrom(c.open, c.title, c.body, c.draft)
		if (err != nil) != c.wantError {
			t.Errorf("%s: error %v, want an error: %v", c.name, err, c.wantError)
		}
		if err == nil && (p != nil) != c.open {
			t.Errorf("%s: proposal %v with -open-pr %v", c.name, p, c.open)
		}
	}
}

// A handed-in token reaches git through the environment of this process,
// after whatever configuration that environment already carries, and never
// as plain text: not argv, not .git/config.
func TestAHandedTokenReachesGitOnlyThroughItsEnvironment(t *testing.T) {
	got := gitAuth([]string{"PATH=/bin", "GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=a", "GIT_CONFIG_KEY_1=b"}, "s3cret")
	want := map[string]bool{"GIT_CONFIG_COUNT=3": true, "GIT_CONFIG_KEY_2=http.https://github.com/.extraheader": true}
	header := "AUTHORIZATION: basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:s3cret"))
	want["GIT_CONFIG_VALUE_2="+header] = true
	for _, kv := range got {
		if !want[kv] {
			t.Errorf("unexpected %q", kv)
		}
		if strings.Contains(kv, "s3cret") {
			t.Errorf("the token is in plain text in %q", kv)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if fresh := gitAuth([]string{"GIT_CONFIG_COUNT=nonsense"}, "t"); fresh[0] != "GIT_CONFIG_COUNT=1" {
		t.Errorf("an unreadable count was built on: %v", fresh)
	}
}

// A token handed in is never printed back: -token is for one minted here.
func TestAHandedTokenIsNeverPrinted(t *testing.T) {
	t.Setenv(tokenEnv, "s3cret")
	err := run("1", "/nowhere", "", true, false, nil)
	if err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("-token with a handed-in token: %v", err)
	}
}

// A failure to open is reported as the branch being out and unopened, so the
// reader knows a rerun finishes it.
func TestAFailureToOpenSaysTheBranchIsPublished(t *testing.T) {
	a, _ := stub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message": "Resource not accessible by integration"}`))
	})
	err := propose(a, "o", "r", "revert/x", &proposal{title: "t", body: "b"})
	if err == nil || !strings.Contains(err.Error(), "revert/x is published") {
		t.Errorf("a refused pull request was reported as %v", err)
	}
	ok, _ := stub(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"html_url": "https://example.test/pull/3"}]`))
	})
	if err := propose(ok, "o", "r", "revert/x", &proposal{title: "t", body: "b"}); err != nil {
		t.Errorf("a pull request already open was an error: %v", err)
	}
}
