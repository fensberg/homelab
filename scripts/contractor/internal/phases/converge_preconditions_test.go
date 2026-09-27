package phases

import (
	"homelab/details/onepassword"
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

// The refusal says nothing was applied and that a re-run cannot help. It used
// to offer a re-run, which is never the tip of main either.
func TestASupersededConvergeSaysReRunningCannotHelp(t *testing.T) {
	msg := (&SupersededError{Head: "aaaaaaa", Tip: "bbbbbbb"}).Error()
	for _, want := range []string{"aaaaaaa", "bbbbbbb", "Nothing was applied", "re-running this run cannot help"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "can be re-run deliberately") {
		t.Error("the refusal still offers a re-run")
	}
}

// A token that is set and refused is reported as refused, with where it is
// replaced; the sign-in advice is for a workstation with no token at all.
func TestARefusedTokenIsNotReportedAsAMissingSignIn(t *testing.T) {
	t.Setenv(onepassword.TokenVariable, "")
	if tokenSet() {
		t.Error("an empty token counts as set")
	}
	t.Setenv(onepassword.TokenVariable, "a-token")
	if !tokenSet() {
		t.Error("a set token was not seen")
	}
	msg := refusedToken().Error()
	for _, want := range []string{"refused", "revoked", "environment"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message does not say %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "op signin") {
		t.Error("a refused token is told to sign in, which cannot help")
	}
}
