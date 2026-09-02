package phases

import (
	"testing"

	"homelab/contractor/internal/run"
)

// The three answers must stay three answers.
//
// The banner these feed used to assert "the estate is untouched by this
// failure" on every converge failure, with no condition beyond "this is a
// converge". True when the run died at Attach; a confident falsehood when it
// died halfway through creating machines - which is the one case where
// somebody most needs to go and look, and the one the message told them not
// to.

// A run that never attached cannot have written anything, and this is a proof
// rather than an assumption: tests/go/repo/converge_order_test.go refuses any
// sequence that runs tofu before attach, so there is no path from an
// unattached workspace to a state write.
func TestEstateChanged_NeverAttachedIsCertainlyUntouched(t *testing.T) {
	ctx := &run.Context{}
	changed, certain := EstateChanged(ctx)
	if changed || !certain {
		t.Fatalf("got changed=%v certain=%v, want false/true: a run that never attached cannot have written state", changed, certain)
	}
}

// Attached, and the state cannot be read back - most likely an interrupted
// apply still holding the lock. That is a third answer. Reporting it as
// "unchanged" is the original bug with extra steps, and reporting it as
// "changed" would send somebody chasing a rollback that may not be needed.
func TestEstateChanged_UnreadableStateIsNotAnAnswer(t *testing.T) {
	// ClusterDir points at nothing, so `tofu state pull` cannot succeed.
	ctx := &run.Context{AttachedOK: true, ClusterDir: t.TempDir()}
	changed, certain := EstateChanged(ctx)
	if certain {
		t.Fatalf("got certain=true (changed=%v) for a state that could not be read; "+
			"\"I do not know\" and \"nothing is wrong\" must not look the same", changed)
	}
}
