package phases

import (
	"os"
	"path/filepath"
	"testing"

	"homelab/contractor/internal/run"
)

// The three answers must stay three answers.
//
// The banner these feed used to assert "the estate is untouched by this
// failure" on every converge failure, with no condition beyond "this is a
// converge". True when the run died at TakeOver; a confident falsehood when it
// died halfway through creating machines - which is the one case where
// somebody most needs to go and look, and the one the message told them not
// to.

// A run that never attached cannot have written anything, and this is a proof
// rather than an assumption: tests/go/repo/converge_order_test.go refuses any
// sequence that runs tofu before take-over, so there is no path from an
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
	// The roots point at nothing, so `tofu state pull` cannot succeed.
	ctx := run.NewContext(t.TempDir(), "site0")
	ctx.TakenOverOK = true
	changed, certain := EstateChanged(ctx)
	if certain {
		t.Fatalf("got certain=true (changed=%v) for a state that could not be read; "+
			"\"I do not know\" and \"nothing is wrong\" must not look the same", changed)
	}
}

// A converge that has only forgotten its cached reads has changed nothing a
// revert would undo, and says so; one that then writes anything says that
// instead.
//
// A tofu stands in on PATH whose state has a serial that rises on every
// write, as the real one does: forgetting a read is a write.
func TestForgettingCachedReadsIsNotAChangeToTheEstate(t *testing.T) {
	bin := t.TempDir()
	script := `#!/bin/sh
n=$(cat serial 2>/dev/null || echo 1)
case "$1 $2" in
  "state list") echo data.something.read ;;
  "state rm") echo $((n + 1)) > serial ;;
  "state pull") echo "{\"version\":4,\"serial\":$n,\"lineage\":\"a-lineage\",\"resources\":[]}" ;;
  "apply "*) echo $((n + 1)) > serial ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(bin, "tofu"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx := run.NewContext(t.TempDir(), "site0")
	for _, root := range ctx.Roots() {
		if err := os.MkdirAll(root.Dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	serial, ok := estateSerial(ctx)
	if !ok {
		t.Fatal("the stand-in's state could not be read")
	}
	ctx.StateSerialAtTakeover, ctx.TakenOverOK = serial, true

	if err := forgetReadsAndMeasureFromHere(ctx); err != nil {
		t.Fatal(err)
	}
	if now, _ := estateSerial(ctx); now == serial {
		t.Fatal("forgetting a read did not write the stand-in's state, so this proves nothing")
	}
	if changed, certain := EstateChanged(ctx); changed || !certain {
		t.Errorf("a converge that had only forgotten its cached reads said changed=%v certain=%v; no revert is offered for a merge that built nothing", changed, certain)
	}

	// Anything written after that is a change.
	if err := run.Cmd(ctx.Dir, "tofu", "apply", "-auto-approve"); err != nil {
		t.Fatal(err)
	}
	if changed, certain := EstateChanged(ctx); !changed || !certain {
		t.Errorf("a converge that then applied said changed=%v certain=%v", changed, certain)
	}
}
