package phases

import (
	"strings"
	"testing"
)

// A queued action is a landmine, and ignition is what steps on it.
//
// The deploy workflow's runner is a pod inside the cluster it converges, so a
// torn-down estate leaves every converge queued with nothing to pick it up.
// They are harmless only while no runner exists - and ignition is precisely
// what ends that, because Flux brings the runner up near the end of the
// sequence. A job queued before the run started then acquires a runner partway
// through it and converges against a half-built estate.

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

// Being unable to ask is not the same as nothing being pending. The run this
// guards is twenty minutes long and unattended in the middle.
func TestIgnitionFailsClosedWhenItCannotAsk(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no gh on the path
	err := CheckBreakGroundPreconditions()
	if err == nil {
		t.Fatal("ignition proceeded without being able to check for queued deploys")
	}
	if !strings.Contains(err.Error(), "cannot show it is safe to start") {
		t.Errorf("the refusal does not say what it could not establish:\n%v", err)
	}
}
