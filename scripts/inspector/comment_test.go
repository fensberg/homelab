package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMain in this package pins githubAPI, so no test reaches the real
// GitHub. See tests/go/repo/pinned_endpoints_test.go for why that is a rule
// rather than a habit.

// Nothing removed, nothing said.
//
// The step summary could afford to print "Nothing." on every push. A comment
// cannot: fifty a day restating what the diff already shows is how a line
// stops being read, and it would take the one that matters with it.
func TestNothingRemovedPostsNothing(t *testing.T) {
	var wrote bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			wrote = true
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	if err := newCommenter(srv.URL, "o/r", "t").post(7, "## nothing", false); err != nil {
		t.Fatalf("post: %v", err)
	}
	if wrote {
		t.Error("a change that removed nothing still wrote to the pull request")
	}
}

// One comment, edited - not one per push.
//
// #160 is exactly the bug of appending one per push. A pull request carrying
// thirty of these is a pull request nobody reads.
func TestASecondReportEditsTheFirst(t *testing.T) {
	var posts, patches int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`[{"id":99,"body":"` + marker + `\nold report"}]`))
			return
		case http.MethodPost:
			posts++
		case http.MethodPatch:
			patches++
			if !strings.HasSuffix(r.URL.Path, "/comments/99") {
				t.Errorf("edited %s, not the comment it had already written", r.URL.Path)
			}
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if err := newCommenter(srv.URL, "o/r", "t").post(7, "## new report", true); err != nil {
		t.Fatalf("post: %v", err)
	}
	if posts != 0 || patches != 1 {
		t.Errorf("posts=%d patches=%d; want the existing comment edited exactly once", posts, patches)
	}
}

// A finding that is no longer true is worse than no finding.
//
// "Four test functions removed" left standing after the removal is undone
// tells the reader something false at the moment they are deciding.
func TestAStaleReportIsRemovedWhenNothingIsLeftToSay(t *testing.T) {
	var deleted string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[{"id":99,"body":"` + marker + `\nfour test functions removed"}]`))
			return
		}
		if r.Method == http.MethodDelete {
			deleted = r.URL.Path
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if err := newCommenter(srv.URL, "o/r", "t").post(7, "## nothing", false); err != nil {
		t.Fatalf("post: %v", err)
	}
	if !strings.HasSuffix(deleted, "/comments/99") {
		t.Errorf("a stale report was left standing; deleted=%q", deleted)
	}
}

// The first report on a pull request is a new comment.
func TestTheFirstReportIsPosted(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var sent map[string]string
		_ = json.Unmarshal(raw, &sent)
		body = sent["body"]
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if err := newCommenter(srv.URL, "o/r", "t").post(7, "## four removed", true); err != nil {
		t.Fatalf("post: %v", err)
	}
	if !strings.Contains(body, marker) {
		t.Error("the comment carries no marker, so the next run cannot find it and will append")
	}
	if !strings.Contains(body, "four removed") {
		t.Errorf("the report did not reach the comment: %q", body)
	}
}

// The token lives an hour and this output lands in a public repository's log.
func TestErrorsDoNotQuoteTheToken(t *testing.T) {
	const tok = "ghs_secretvalue"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad credentials for "+tok, http.StatusUnauthorized)
	}))
	defer srv.Close()

	err := newCommenter(srv.URL, "o/r", tok).post(7, "x", true)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), tok) {
		t.Errorf("the error quotes the token: %v", err)
	}
}
