package main

import (
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
	url := runsURL("owner/repo", "integration-tests.yml", "event=schedule")
	if !strings.Contains(url, "/actions/workflows/integration-tests.yml/runs") {
		t.Errorf("asked %s - that is every scheduled run, including the patrol's own", url)
	}
	if !strings.Contains(url, "event=schedule") {
		t.Errorf("asked %s, which drops the event filter", url)
	}
}

func TestTheQueueQuestionIsAskedAboutTheWholeRepository(t *testing.T) {
	url := runsURL("owner/repo", "", "status=queued")
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
