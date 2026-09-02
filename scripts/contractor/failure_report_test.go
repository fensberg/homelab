package main

import (
	"testing"

	"homelab/contractor/internal/run"
)

// A caller automating recovery needs to know whether reverting the change is
// exact or a guess, and an exit code is the only channel a workflow can read
// without parsing prose. See #130.

// Nothing attached, so nothing was applied: the estate is provably untouched
// and a revert is exact.
func TestPreexistingFailure_UntouchedGetsItsOwnExitCode(t *testing.T) {
	ctx := &run.Context{Site: "site0"}
	if got := reportPreexistingFailure(ctx, "converge"); got != exitUntouched {
		t.Fatalf("got exit %d, want %d for a run that never attached", got, exitUntouched)
	}
}

// Attached, and the state cannot be read. Not knowing must exit the same way
// as "it changed", because both mean a revert is a guess - the safe direction
// for anything automating on this.
func TestPreexistingFailure_CannotTellIsTreatedAsChanged(t *testing.T) {
	ctx := &run.Context{Site: "site0", AttachedOK: true, ClusterDir: t.TempDir()}
	if got := reportPreexistingFailure(ctx, "converge"); got != exitMayHaveChanged {
		t.Fatalf("got exit %d, want %d when the estate's state could not be read", got, exitMayHaveChanged)
	}
}

// The two codes must stay distinct, which is the entire point of having them.
func TestPreexistingFailure_TheCodesAreDistinct(t *testing.T) {
	if exitUntouched == exitMayHaveChanged {
		t.Fatal("the two outcomes share an exit code, so nothing downstream can tell them apart")
	}
}
