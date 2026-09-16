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

// A run the listing calls queued and GitHub calls finished is not a hazard.
//
// THE RUN THAT PRODUCED THIS. break-ground refused to start:
//
//	[FAIL] HALTED: 5 deploy run(s) would converge site0 during this ignition.
//	  #152 (main, pending) - gh run cancel 34532646382
//	  ... four more
//
// Every one of the five commands it printed was rejected with "Cannot cancel a
// workflow run that is completed" (#362). The listing endpoint reported them
// pending; the runs themselves were concluded. The operator ran five commands,
// had all five refused, and then had to decide alone whether to ignore a halt
// that describes itself as a landmine.
//
// The halt was right in principle and useless in practice, which is the worse
// of the two failures: a gate that fires on stale data and hands over commands
// that do not work teaches the operator to route around it, and that is
// strictly worse than not having the gate.
func TestARunTheListingCallsPendingButGitHubHasFinishedIsDropped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/runs"):
			_, _ = w.Write([]byte(`{"workflow_runs":[
				{"id":44,"run_number":152,"status":"pending","head_branch":"main"},
				{"id":55,"run_number":153,"status":"queued","head_branch":"main"}
			]}`))
		case strings.HasSuffix(r.URL.Path, "/jobs"):
			_, _ = w.Write([]byte(`{"jobs":[{"name":"Converge site0"}]}`))
		case strings.HasSuffix(r.URL.Path, "/44"):
			// What GitHub actually says about it.
			_, _ = w.Write([]byte(`{"status":"completed","conclusion":"cancelled"}`))
		default:
			_, _ = w.Write([]byte(`{"status":"queued","conclusion":null}`))
		}
	}))
	defer srv.Close()

	restore := pointAt(t, srv.URL, "fensberg/homelab")
	defer restore()

	runs, err := fetchActiveRuns()
	if err != nil {
		t.Fatalf("fetchActiveRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf(`got %d run(s), want only the one that is genuinely still queued: %+v

A concluded run cannot be cancelled and will never converge anything, so
halting on it costs an operator five refused commands and the gate its
credibility.`, len(runs), runs)
	}
	if runs[0].Number != 153 {
		t.Errorf("kept run #%d; #152 is the concluded one", runs[0].Number)
	}
}

// A run whose status cannot be confirmed is kept, and said to be unconfirmed.
//
// "I could not ask" is not "nothing is pending". The window being guarded is
// twenty minutes long with nobody watching the middle of it, and a converge
// acquiring a runner partway through an ignition applies against a half-built
// estate - so an unreadable run stays on the halt. What changes is that the
// halt says so, instead of handing over a command that may be refused.
func TestARunWhoseStatusCannotBeConfirmedIsKeptAndFlagged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/runs"):
			_, _ = w.Write([]byte(`{"workflow_runs":[
				{"id":66,"run_number":160,"status":"queued","head_branch":"main"}
			]}`))
		case strings.HasSuffix(r.URL.Path, "/jobs"):
			_, _ = w.Write([]byte(`{"jobs":[{"name":"Converge site0"}]}`))
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()

	restore := pointAt(t, srv.URL, "fensberg/homelab")
	defer restore()

	runs, err := fetchActiveRuns()
	if err != nil {
		t.Fatalf("fetchActiveRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf(`an unreadable run was dropped: %+v

Failing open here means an ignition proceeds while a converge is queued behind
it, which is the exact hazard this precondition exists for.`, runs)
	}
	if !runs[0].Stale {
		t.Error("the run was kept but not flagged as unconfirmed, so the halt prints " +
			"a cancel command as though it were known to work")
	}

	err = noPendingDeploysForSite("site0")
	if err == nil {
		t.Fatal("the ignition was allowed to start with a run whose status is unknown")
	}
	if !strings.Contains(err.Error(), "could not be confirmed") {
		t.Errorf(`the halt does not say the status is unconfirmed:

%v
An operator whose cancel command is refused needs to know the guard already
suspected that, or they learn to distrust the guard rather than the run.`, err)
	}
}
