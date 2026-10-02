package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 11, 15, 0, 0, 0, time.UTC)

func ago(d time.Duration) time.Time { return now.Add(-d) }

// A run waiting for someone to approve a deployment environment is not a run
// the estate failed to pick up, and counting it as one is what kept the patrol
// red for two days while it reported the wrong cause.
//
// On 2026-09-11 the patrol failed every scheduled run with "5 run(s) queued
// longer than 30m, oldest 53h56m - work is not being picked up". The runner was
// fine. All five were `waiting`: deploy runs gated on an environment nobody
// approved, and a nightly gated the same way. GitHub notifies whoever can
// approve those; the patrol exists for the case nobody is notified about, which
// is work that cannot start because nothing will run it.
func TestOnlyRunsNobodyCanStartCountAsStuck(t *testing.T) {
	runs := []run{
		{Status: "queued", CreatedAt: ago(2 * time.Hour)},   // stuck: no runner took it
		{Status: "pending", CreatedAt: ago(3 * time.Hour)},  // stuck: behind a concurrency group that never frees
		{Status: "waiting", CreatedAt: ago(54 * time.Hour)}, // not stuck: waiting for an approval
		{Status: "queued", CreatedAt: ago(5 * time.Minute)}, // queued, but not for long
	}

	count, worst := stuck(runs, now, 30*time.Minute)

	if count != 2 {
		t.Errorf("counted %d stuck run(s), want 2 - a run waiting for approval is not stuck", count)
	}
	if worst != 3*time.Hour {
		t.Errorf("oldest stuck run is %s, want 3h0m0s - the 54h approval wait must not be the one reported", worst)
	}
}

func TestNothingOldEnoughIsNotStuck(t *testing.T) {
	runs := []run{{Status: "queued", CreatedAt: ago(10 * time.Minute)}}
	if count, _ := stuck(runs, now, 30*time.Minute); count != 0 {
		t.Errorf("counted %d, want 0", count)
	}
}

func TestOnlyASuccessCountsAsTheNightlyHavingRun(t *testing.T) {
	runs := []run{
		{Status: "completed", Conclusion: "failure", UpdatedAt: ago(1 * time.Hour)},
		{Status: "in_progress", UpdatedAt: ago(30 * time.Minute)},
		{Status: "completed", Conclusion: "success", UpdatedAt: ago(40 * time.Hour)},
		{Status: "completed", Conclusion: "cancelled", UpdatedAt: ago(2 * time.Hour)},
	}
	if got := newestSuccess(runs); !got.Equal(ago(40 * time.Hour)) {
		t.Errorf("newest success is %s, want the one 40h ago", now.Sub(got))
	}
	if got := newestSuccess(nil); !got.IsZero() {
		t.Errorf("no runs gave %s, want the zero time", got)
	}
}

// The nightly check must ask about the drift check, not about scheduled runs in
// general - because the patrol is itself a scheduled workflow.
//
// It asked `/actions/runs?event=schedule`, which returns every scheduled run in
// the repository. That was only accidentally correct: it answered "no success
// in 101h" because the patrol was failing too. The moment the patrol went
// green, its own successes would have satisfied a check whose whole purpose is
// noticing that the nightly has stopped - a watcher reporting on itself and
// calling it the thing it watches.
func TestTheNightlyQuestionIsAskedAboutTheNightly(t *testing.T) {
	url := runsURL("", "owner/repo", "integration-tests.yml", "event=schedule")
	if !strings.Contains(url, "/actions/workflows/integration-tests.yml/runs") {
		t.Errorf("asked %s - that is every scheduled run, including the patrol's own", url)
	}
	if !strings.Contains(url, "event=schedule") {
		t.Errorf("asked %s, which drops the event filter", url)
	}
}

func TestTheQueueQuestionIsAskedAboutTheWholeRepository(t *testing.T) {
	url := runsURL("", "owner/repo", "", "status=queued")
	if strings.Contains(url, "/workflows/") {
		t.Errorf("asked %s - a stuck queue anywhere is the fault, not only in one workflow", url)
	}
}

// A check that could not ask anything must not let the patrol say the estate is
// healthy. It used to: an unreachable API gave every check a "skip", skips did
// not count, and the summary read "the estate is answering for itself" over a
// patrol that had seen nothing at all.
func TestAPatrolThatCouldNotLookDoesNotReportHealth(t *testing.T) {
	results := []result{
		{name: "a", status: "ok"},
		{name: "b", status: "unknown", detail: "could not ask GitHub"},
	}
	if failed := unhealthy(results); failed != 1 {
		t.Errorf("%d check(s) counted against health, want 1 - not knowing is not ok", failed)
	}
	if failed := unhealthy([]result{{status: "ok"}, {status: "skip"}}); failed != 0 {
		t.Errorf("%d counted, want 0 - a skip is 'nothing to check yet', not ignorance", failed)
	}
}

// A run held at an environment approval is somebody's turn, not a fault.
//
// The patrol excluded the `waiting` status for exactly this reason, and it
// still went red: a run stopped at an approval frequently reports `pending`,
// which the stuck check counts. Three deploy runs held on the management
// environment failed the patrol on 2026-09-20 with "work is not being picked
// up", while the runner was healthy and the work was simply waiting for a
// person.
//
// So the status picks candidates and GitHub says which are somebody's turn.
func TestWaitingOnAPerson_SeparatesApprovalsFromAStuckQueue(t *testing.T) {
	runs := []run{{ID: 1}, {ID: 2}, {ID: 3}}
	approvals := map[int64]bool{1: true, 3: true}

	held, rest := waitingOnAPerson(runs, func(id int64) (bool, error) {
		return approvals[id], nil
	})

	if len(held) != 2 || held[0].ID != 1 || held[1].ID != 3 {
		t.Errorf("held = %v, want the two awaiting approval", held)
	}
	if len(rest) != 1 || rest[0].ID != 2 {
		t.Errorf("rest = %v, want only the run nobody is holding", rest)
	}
}

// "I could not ask" is not "nobody is waiting".
//
// A patrol that drops what it failed to ask about reports health it never
// established - the same rule that makes an unreachable GitHub an "unknown"
// rather than a pass.
func TestWaitingOnAPerson_KeepsRunsItCouldNotAskAbout(t *testing.T) {
	runs := []run{{ID: 7}}

	held, rest := waitingOnAPerson(runs, func(int64) (bool, error) {
		return false, errors.New("GitHub answered 502")
	})

	if len(held) != 0 {
		t.Errorf("a run whose status could not be established was reported as held: %v", held)
	}
	if len(rest) != 1 {
		t.Fatalf("rest = %v, want the run kept in the count", rest)
	}
}

// The converge is asked about by its workflow's file, whatever its runs are
// called. It was found by name among every run on main, and a run is called
// whatever its workflow's run-name makes it: once the converge named its runs
// for the change they converge, none matched, and the patrol reported the
// last run that still had the old name - a failure a week old - through five
// converges that succeeded.
func TestTheConvergeIsFoundByItsWorkflowAndNotByWhatItsRunsAreCalled(t *testing.T) {
	answer := func(t *testing.T, runs string) *client {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/repos/owner/repo/actions/workflows/the-converge.yml/runs" {
				// Every run on main, as it was asked before: the stale
				// failure is all a search by name would find.
				_, _ = w.Write([]byte(`{"workflow_runs":[{"name":"Deploy Infrastructure","status":"completed","conclusion":"failure","created_at":"2026-09-27T14:15:27Z"}]}`))
				return
			}
			if q := r.URL.Query(); q.Get("branch") != "main" || q.Get("event") != "push" {
				t.Errorf("asked with %s, which is not the converge of a merge to main", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"workflow_runs":[` + runs + `]}`))
		}))
		t.Cleanup(srv.Close)
		return &client{repo: "owner/repo", token: "t", api: srv.URL}
	}
	run := func(name, status, conclusion, created string) string {
		return `{"name":"` + name + `","status":"` + status + `","conclusion":"` + conclusion + `","created_at":"` + created + `","updated_at":"` + created + `"}`
	}
	for label, tc := range map[string]struct{ runs, want string }{
		"the newest succeeded, under a name of its own": {
			run("Converge: a change", "completed", "success", "2026-10-02T16:40:15Z") + "," +
				run("Converge: an older one", "completed", "failure", "2026-10-01T10:00:00Z"), "ok"},
		"the newest failed": {
			run("Converge: an older one", "completed", "success", "2026-10-01T10:00:00Z") + "," +
				run("Converge: a change", "completed", "failure", "2026-10-02T16:40:15Z"), "fail"},
		"the newest is still running": {
			run("Converge: a change", "in_progress", "", "2026-10-02T16:40:15Z") + "," +
				run("Converge: an older one", "completed", "success", "2026-10-01T10:00:00Z"), "ok"},
		"none has finished": {run("Converge: a change", "queued", "", "2026-10-02T16:40:15Z"), "skip"},
		"none at all":       {"", "skip"},
	} {
		if got := answer(t, tc.runs).lastConvergeDidNotFail("the-converge.yml"); got.status != tc.want {
			t.Errorf("%s: %s (%s), want %s", label, got.status, got.detail, tc.want)
		}
	}
	// And GitHub not answering is not knowing, which is not health.
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer down.Close()
	if got := (&client{repo: "owner/repo", token: "t", api: down.URL}).lastConvergeDidNotFail("the-converge.yml"); got.status != "unknown" {
		t.Errorf("with GitHub not answering: %s", got.status)
	}
}
