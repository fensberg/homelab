package phases

import (
	"strings"
	"testing"
)

// A converge must refuse to apply a commit that is no longer current.
//
// The runner is a pod inside the cluster it converges, so tearing the estate
// down leaves every converge queued with nothing to pick it up. The workflow
// cannot bound that - timeout-minutes only counts once a job is running - and
// the danger is not the waiting: it is a runner appearing later and applying
// the commit the job was queued at. That has happened, with the oldest of five
// blocked refs pinned to a commit two days behind main.

func TestAConvergeChecksItIsTheTipOfMain(t *testing.T) {
	checks := ConvergePreconditions()
	if len(checks) == 0 {
		t.Fatal("a converge has no preconditions, so a job queued days ago will apply " +
			"whatever it was queued at the moment a runner appears")
	}
	found := false
	for _, c := range checks {
		if strings.Contains(c.Name, "tip of main") {
			found = true
		}
		if c.Check == nil {
			t.Errorf("precondition %q has no check", c.Name)
		}
	}
	if !found {
		t.Error("no precondition asks whether this converge is still current, which is " +
			"the one question a queued job cannot answer for itself")
	}
}

// Outside CI this is a no-op: a local converge from an older commit is a
// deliberate act by somebody present and watching. The failure being guarded
// against is specifically the unattended one.
func TestPreconditionsDoNotBlockALocalConverge(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "")
	if err := CheckConvergePreconditions(); err != nil {
		t.Fatalf("a local converge was refused: %v", err)
	}
}

// Being unable to establish that a converge is current is not the same as it
// being current. "I could not tell" and "yes" must not behave alike, which is
// this estate's standing rule about checks that can be off.
func TestAnUndeterminableTipFailsClosed(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	// No git remote reachable from a scratch directory with no repository.
	t.Chdir(t.TempDir())

	err := CheckConvergePreconditions()
	if err == nil {
		t.Fatal("a converge that could not read the tip of main was allowed to proceed")
	}
	if !strings.Contains(err.Error(), "cannot show it is current") &&
		!strings.Contains(err.Error(), "cannot be shown to be current") {
		t.Errorf("the refusal does not say why it refused:\n%v", err)
	}
}
