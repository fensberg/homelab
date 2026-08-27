package main

import (
	"strings"
	"testing"

	"homelab/ignite/internal/phases"
)

// selectPhases decides what a run actually does. Every other safety property
// in this program is downstream of it: main.go asks whether the selection
// contains "compute" to decide whether a failed run needs an emergency
// destroy, and whether it contains "sterilize" to decide whether to wipe the
// workspace on the way out. A wrong answer here is not a wrong phase list, it
// is an orphaned VM or a leaked secret.

func TestSelectPhases_DefaultsToTheFullSequence(t *testing.T) {
	got, err := selectPhases("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(got, ",") != strings.Join(phases.AllPhases, ",") {
		t.Errorf("got %v, want the full sequence %v", got, phases.AllPhases)
	}
}

// The returned slice must not alias AllPhases: main.go runs
// slices.DeleteFunc over it for -skip-overlay, which writes in place. A
// shared backing array would let one run's flag mutate the package-level
// sequence for everything after it.
func TestSelectPhases_DoesNotAliasThePackageSequence(t *testing.T) {
	got, err := selectPhases("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	original := phases.AllPhases[0]
	got[0] = "mutated"
	if phases.AllPhases[0] != original {
		t.Fatalf("mutating the returned slice changed phases.AllPhases[0] from %q to %q", original, phases.AllPhases[0])
	}
}

func TestSelectPhases_SinglePhase(t *testing.T) {
	got, err := selectPhases("compute", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "compute" {
		t.Errorf("got %v, want exactly [compute]", got)
	}
}

func TestSelectPhases_FromIsInclusiveAndRunsToTheEnd(t *testing.T) {
	got, err := selectPhases("", "migrate")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"migrate", "backup", "sterilize"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}

// -from render is the whole sequence, which is worth pinning explicitly: it
// is the boundary case where an off-by-one would silently skip Render and
// leave every later phase reading a config that was never written.
func TestSelectPhases_FromTheFirstPhaseIsTheWholeSequence(t *testing.T) {
	got, err := selectPhases("", phases.AllPhases[0])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(phases.AllPhases) {
		t.Errorf("got %d phases, want all %d", len(got), len(phases.AllPhases))
	}
}

func TestSelectPhases_FromTheLastPhaseIsJustThatPhase(t *testing.T) {
	last := phases.AllPhases[len(phases.AllPhases)-1]
	got, err := selectPhases("", last)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != last {
		t.Errorf("got %v, want exactly [%s]", got, last)
	}
}

// -phase takes precedence over -from when both are given. Not an arbitrary
// choice to pin: the alternative reading (run -from, ignore -phase) would
// turn a command someone believed was a single safe step into a full run.
func TestSelectPhases_PhaseWinsOverFrom(t *testing.T) {
	got, err := selectPhases("verify", "render")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "verify" {
		t.Errorf("got %v, want exactly [verify] - the narrower flag must win", got)
	}
}

func TestSelectPhases_UnknownNamesAreRejectedAndListTheValidOnes(t *testing.T) {
	for _, tc := range []struct{ phase, from string }{
		{"nope", ""},
		{"", "nope"},
	} {
		_, err := selectPhases(tc.phase, tc.from)
		if err == nil {
			t.Errorf("selectPhases(%q, %q): expected an error", tc.phase, tc.from)
			continue
		}
		// The message has to name the alternatives - a bare "unknown phase"
		// leaves the operator guessing at spelling during an outage.
		if !strings.Contains(err.Error(), "compute") {
			t.Errorf("selectPhases(%q, %q): the error should list the valid phases, got: %v", tc.phase, tc.from, err)
		}
	}
}

// Case sensitivity is worth pinning rather than discovering: the phase names
// are printed lower-case everywhere, and a run that silently accepted
// "Compute" would create infrastructure from what the operator typed rather
// than from what the program documents.
func TestSelectPhases_IsCaseSensitive(t *testing.T) {
	if _, err := selectPhases("Compute", ""); err == nil {
		t.Error("expected -phase Compute to be rejected; the documented names are lower-case")
	}
}

// -destroy is not a phase, and must not look like one. A command reading
// "destroy, but only the verify phase" would be understood by somebody as a
// safe thing to run, so the combination is refused rather than interpreted.
func TestDestroyFlagsOK(t *testing.T) {
	if err := destroyFlagsOK(true, "", ""); err != nil {
		t.Errorf("plain -destroy should be accepted, got: %v", err)
	}
	if err := destroyFlagsOK(false, "verify", "render"); err != nil {
		t.Errorf("without -destroy the phase selectors are none of this function's business, got: %v", err)
	}
	for _, tc := range []struct{ phase, from string }{
		{"verify", ""},
		{"", "render"},
		{"compute", "render"},
	} {
		if err := destroyFlagsOK(true, tc.phase, tc.from); err == nil {
			t.Errorf("destroyFlagsOK(true, %q, %q) was accepted; -destroy must not compose with the phase selectors", tc.phase, tc.from)
		}
	}
}
