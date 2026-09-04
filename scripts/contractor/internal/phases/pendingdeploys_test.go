package phases

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// The survey asks GitHub over HTTP, not by shelling out.
//
// The first integration-tier run in this repository's history halted here with
// `exec: "gh": executable file not found in $PATH`. Installing gh would not
// have fixed it either - that job holds only contents:read, and listing runs
// needs actions:read - so the dependency came out instead.
func TestActiveRunsAreReadFromTheAPI(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, "/runs"):
			_, _ = w.Write([]byte(`{"workflow_runs":[
				{"id":11,"run_number":1,"status":"completed","head_branch":"main"},
				{"id":22,"run_number":2,"status":"waiting","head_branch":"main"},
				{"id":33,"run_number":3,"status":"queued","head_branch":"epoch/08"}
			]}`))
		default:
			_, _ = w.Write([]byte(`{"jobs":[{"name":"Converge site0"}]}`))
		}
	}))
	defer srv.Close()

	restore := pointAt(t, srv.URL, "fensberg/homelab")
	defer restore()

	runs, err := fetchActiveRuns()
	if err != nil {
		t.Fatalf("fetchActiveRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want the two unfinished ones: %+v", len(runs), runs)
	}
	if len(asked) == 0 || !strings.Contains(asked[0], "deploy-infrastructure.yml") {
		t.Errorf("did not ask for the deploy workflow's runs: %v", asked)
	}
}

// A run held at an approval gate is unfinished, and would acquire a runner the
// moment one appears. It counts.
func TestAWaitingRunCountsAsPending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/runs") {
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":22,"run_number":2,"status":"waiting","head_branch":"main"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"jobs":[{"name":"Converge site0"}]}`))
	}))
	defer srv.Close()
	restore := pointAt(t, srv.URL, "o/r")
	defer restore()

	runs, err := fetchActiveRuns()
	if err != nil {
		t.Fatalf("fetchActiveRuns: %v", err)
	}
	if got := pendingForSite(runs, "site0"); len(got) != 1 {
		t.Errorf("a waiting run targeting site0 was not treated as pending: %+v", runs)
	}
}

// Fail closed. "I could not ask" is not "nothing is pending".
//
// A 403 here is what a caller without actions:read gets, which is the same
// refusal gh would have produced. It must be an error, never an empty list -
// an empty list means the ignition proceeds.
func TestARefusalIsAnErrorAndNotAnEmptyList(t *testing.T) {
	for _, code := range []int{403, 404, 500} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		restore := pointAt(t, srv.URL, "o/r")
		_, err := fetchActiveRuns()
		restore()
		srv.Close()

		if err == nil {
			t.Errorf("HTTP %d was read as 'nothing is pending'", code)
		}
	}
}

// A run whose jobs cannot be read keeps an empty job list, which
// pendingForSite treats as pending - the safe reading, preserved from the
// version that shelled out.
func TestARunWhoseJobsCannotBeReadIsStillPending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/runs") {
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":22,"run_number":2,"status":"queued","head_branch":"main"}]}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	restore := pointAt(t, srv.URL, "o/r")
	defer restore()

	runs, err := fetchActiveRuns()
	if err != nil {
		t.Fatalf("fetchActiveRuns: %v", err)
	}
	if len(runs) != 1 || len(runs[0].Jobs) != 0 {
		t.Fatalf("expected one run with no jobs read: %+v", runs)
	}
	if len(pendingForSite(runs, "site0")) != 1 {
		t.Error("a run whose jobs are unknown was assumed harmless")
	}
}

func TestRepoSlugPrefersTheEnvironment(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "fensberg/homelab")
	got, err := repoSlug()
	if err != nil || got != "fensberg/homelab" {
		t.Errorf("got %q, %v", got, err)
	}
}

// pointAt aims the API at a test server and states every input it depends on.
func pointAt(t *testing.T, url, slug string) func() {
	t.Helper()
	was := githubAPI
	githubAPI = url
	t.Setenv("GITHUB_REPOSITORY", slug)
	// A token from the developer's environment would change what is sent.
	_ = os.Unsetenv("GITHUB_TOKEN")
	return func() { githubAPI = was }
}
