package phases

import (
	"strings"
	"testing"
)

// A queued action is a landmine, and ignition is what steps on it.
//
// The deploy workflow's runner is a pod inside the cluster it converges, so a
// torn-down estate leaves converges queued with nothing to pick them up. They
// are harmless only while no runner exists - and ignition is precisely what
// ends that, because Flux brings the runner up near the end of the sequence.

func TestIgnitionChecksForQueuedDeploys(t *testing.T) {
	checks := BreakGroundPreconditions()
	if len(checks) == 0 {
		t.Fatal("ignition surveys nothing before it starts building, so a queued deploy " +
			"will fire into the middle of it the moment Flux brings a runner up")
	}
	found := false
	for _, c := range checks {
		if strings.Contains(c.Name, "queued") {
			found = true
		}
		if c.Check == nil {
			t.Errorf("precondition %q has no check", c.Name)
		}
	}
	if !found {
		t.Error("nothing asks whether a deploy is queued, which is the one hazard " +
			"ignition creates the conditions for rather than merely encountering")
	}
}

// Being unable to ask is not the same as nothing being pending. The window
// this guards is twenty minutes long and unattended in the middle.
func TestIgnitionFailsClosedWhenItCannotAsk(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no gh on the path
	err := CheckBreakGroundPreconditions("site0")
	if err == nil {
		t.Fatal("ignition proceeded without being able to check for queued deploys")
	}
	if !strings.Contains(err.Error(), "cannot show it is safe to start") {
		t.Errorf("the refusal does not say what it could not establish:\n%v", err)
	}
}

// --- the scoping, which is the part that must not overreach -----------------
//
// The converge job is a matrix over sites. A run queued while site0 is being
// converged says nothing about whether site1 is safe to ignite, and a guard
// that refuses to build a second site because the first one is busy is a guard
// somebody switches off - after which it is not protecting the first site
// either.

func TestARunForAnotherSiteDoesNotBlockThisOne(t *testing.T) {
	runs := []activeRun{{
		Number: 41, Status: "queued", HeadBranch: "main",
		Jobs: []string{"What changed", "Converge site0"},
	}}
	if got := pendingForSite(runs, "site10"); len(got) != 0 {
		t.Fatalf("a run that only converges site0 blocked an ignition of site10: %v", got)
	}
}

func TestARunForThisSiteBlocksIt(t *testing.T) {
	runs := []activeRun{{
		Number: 42, Status: "queued", HeadBranch: "main",
		Jobs: []string{"What changed", "Converge site0", "Converge site10"},
	}}
	if got := pendingForSite(runs, "site10"); len(got) != 1 {
		t.Fatalf("a run whose matrix includes site10 did not block an ignition of it: %v", got)
	}
}

// A finished run is not a hazard however many sites it touched.
func TestACompletedRunIsNotPending(t *testing.T) {
	runs := []activeRun{{
		Number: 43, Status: "completed", Jobs: []string{"Converge site0"},
	}}
	if got := pendingForSite(runs, "site0"); len(got) != 0 {
		t.Fatalf("a completed run was treated as pending: %v", got)
	}
}

// A run whose jobs could not be read counts as pending. Not knowing what a
// queued job will do is not the same as knowing it will do nothing.
func TestARunWithUnknownJobsIsTreatedAsPending(t *testing.T) {
	runs := []activeRun{{Number: 44, Status: "queued"}}
	if got := pendingForSite(runs, "site0"); len(got) != 1 {
		t.Fatalf("a queued run with unreadable jobs was assumed harmless: %v", got)
	}
}

// A status GitHub adds later must not be silently read as harmless, which is
// why the set is listed rather than inferred from "not completed".
func TestUnfinishedStatusesAreListedNotInferred(t *testing.T) {
	for _, s := range []string{"queued", "in_progress", "waiting", "requested", "pending"} {
		if !unfinished[s] {
			t.Errorf("%q is not treated as unfinished, so a run in that state would be ignored", s)
		}
	}
}
