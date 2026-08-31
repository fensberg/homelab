package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"homelab/contractor/internal/phases"
	"homelab/contractor/internal/run"
)

// selectPhases decides what a run actually does. Every other safety property
// in this program is downstream of it: main.go asks whether the selection
// contains "compute" to decide whether a failed run needs an emergency
// destroy, and whether it contains "sterilize" to decide whether to wipe the
// workspace on the way out. A wrong answer here is not a wrong phase list, it
// is an orphaned VM or a leaked secret.

func TestSelectPhases_DefaultsToTheFullSequence(t *testing.T) {
	got, err := selectPhases("", "", "break-ground")
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
	got, err := selectPhases("", "", "break-ground")
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
	got, err := selectPhases("compute", "", "break-ground")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "compute" {
		t.Errorf("got %v, want exactly [compute]", got)
	}
}

func TestSelectPhases_FromIsInclusiveAndRunsToTheEnd(t *testing.T) {
	got, err := selectPhases("", "migrate", "break-ground")
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
	got, err := selectPhases("", phases.AllPhases[0], "break-ground")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(phases.AllPhases) {
		t.Errorf("got %d phases, want all %d", len(got), len(phases.AllPhases))
	}
}

func TestSelectPhases_FromTheLastPhaseIsJustThatPhase(t *testing.T) {
	last := phases.AllPhases[len(phases.AllPhases)-1]
	got, err := selectPhases("", last, "break-ground")
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
	got, err := selectPhases("verify", "render", "break-ground")
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
		_, err := selectPhases(tc.phase, tc.from, "break-ground")
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
	if _, err := selectPhases("Compute", "", "break-ground"); err == nil {
		t.Error("expected -phase Compute to be rejected; the documented names are lower-case")
	}
}

// The property standaloneFlagsOK used to enforce, now enforced by which flags
// exist at all. A command reading "destroy, but only the verify phase" would be
// understood by somebody as a safe thing to run, so it must not parse.
func TestFlagsFor_VerbsWithoutPhasesDoNotDefinePhaseSelectors(t *testing.T) {
	for _, verb := range []string{"demolish", "restore", "kubeconfig", "check-inventory"} {
		o := flagsFor(verb)
		for _, name := range []string{"phase", "from"} {
			if o.fs.Lookup(name) != nil {
				t.Errorf("verb %q defines -%s; it runs no sequence, so that combination describes something that cannot happen", verb, name)
			}
		}
		if o.phase != nil || o.from != nil {
			t.Errorf("verb %q carries a phase selector", verb)
		}
	}
}

func TestFlagsFor_SequencedVerbsDefinePhaseSelectors(t *testing.T) {
	for _, verb := range []string{"break-ground", "converge"} {
		o := flagsFor(verb)
		for _, name := range []string{"phase", "from", "whatif"} {
			if o.fs.Lookup(name) == nil {
				t.Errorf("verb %q does not define -%s, but it runs a sequence", verb, name)
			}
		}
	}
}

// A converge never destroys on failure, so there is nothing for
// -keep-on-failure to opt out of. Offering it would imply the default is the
// other way round, which is the opposite of true.
func TestFlagsFor_ConvergeHasNoKeepOnFailure(t *testing.T) {
	if flagsFor("converge").fs.Lookup("keep-on-failure") != nil {
		t.Error("converge defines -keep-on-failure, implying it might destroy on failure; it never does")
	}
	if flagsFor("break-ground").fs.Lookup("keep-on-failure") == nil {
		t.Error("ignite does not define -keep-on-failure, so a failed run cannot be kept for debugging")
	}
}

// -confirm belongs to destroy alone. Anywhere else it would be a flag that
// reads like a safety check and does nothing.
func TestFlagsFor_ConfirmBelongsToDestroyOnly(t *testing.T) {
	if flagsFor("demolish").fs.Lookup("confirm") == nil {
		t.Fatal("destroy does not define -confirm, so the typo guard is gone")
	}
	for _, verb := range []string{"break-ground", "converge", "restore", "kubeconfig", "check-inventory"} {
		if flagsFor(verb).fs.Lookup("confirm") != nil {
			t.Errorf("verb %q defines -confirm, which would read as a safety check while doing nothing", verb)
		}
	}
}

// Every verb acts on a site, and every verb must therefore accept one.
func TestFlagsFor_EveryVerbTakesASite(t *testing.T) {
	for _, verb := range knownVerbs {
		if flagsFor(verb).fs.Lookup("site") == nil {
			t.Errorf("verb %q does not accept -site", verb)
		}
	}
}

// A converge indexes a different sequence, and the differences are the whole
// safety story: it attaches to state that already exists, and it must never
// run migrate, whose -force-copy would overwrite that state with whatever this
// workspace happened to hold.
func TestSelectPhases_ConvergeNeverMigrates(t *testing.T) {
	got, err := selectPhases("", "", "converge")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if slices.Contains(got, "migrate") {
		t.Fatal("converge included the migrate phase; -force-copy would overwrite the estate's own state with an empty workspace")
	}
	if !slices.Contains(got, "attach") {
		t.Fatal("converge did not include attach, so it would start from an empty workspace and plan a second estate")
	}
	if got[0] != "render" {
		t.Fatalf("converge must render first - every later phase needs the config, and it is the credential check. Got %q", got[0])
	}
}

// attach exists only in a converge and migrate only in an ignition. Accepting
// either against the wrong sequence would let somebody ask for a phase that
// cannot happen in the run they are actually starting.
func TestSelectPhases_SequencesDoNotLeak(t *testing.T) {
	if _, err := selectPhases("attach", "", "break-ground"); err == nil {
		t.Error("ignition accepted -phase attach, which only exists in a converge")
	}
	if _, err := selectPhases("migrate", "", "converge"); err == nil {
		t.Error("converge accepted -phase migrate, which would overwrite the estate's state")
	}
}

// Ignition ends by sterilizing, and so must a converge: the workstation should
// hold no state and no secrets afterwards either way.
func TestSelectPhases_ConvergeStillSterilizes(t *testing.T) {
	got, err := selectPhases("", "", "converge")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got[len(got)-1] != "sterilize" {
		t.Fatalf("a converge must end sterilized, got %q", got[len(got)-1])
	}
}

// plan is its own sequence for the same reason converge is: what a run may
// assume differs. A plan must reach attach - a plan against no state is a plan
// to build everything, which is the opposite of the answer being asked for -
// and must contain nothing that changes the estate.
func TestSelectPhases_PlanChangesNothing(t *testing.T) {
	got, err := selectPhases("", "", "plan")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Contains(got, "attach") {
		t.Fatal("plan does not attach, so it would plan against an empty workspace and report building the whole estate")
	}
	for _, mutating := range []string{"compute", "cluster", "migrate", "backup", "hypervisor", "overlay"} {
		if slices.Contains(got, mutating) {
			t.Errorf("plan includes %q, which changes something. A plan changes nothing.", mutating)
		}
	}
	if got[len(got)-1] != "sterilize" {
		t.Fatalf("a plan must end sterilized - it renders secrets and saves a plan file full of attribute values. Got %q", got[len(got)-1])
	}
}

// The failure path calls EmergencyDestroy, which tears down whatever the
// state describes. That is correct for an ignition: a run that broke halfway
// has built VMs nothing is tracking, and destroying them is the safe end.
//
// It is catastrophic for anything else. `contractor plan` creates nothing at all,
// yet it attaches to the estate's state - so when its plan timed out, the
// failure path was one branch away from destroying a running estate that the
// command had only ever read. This asserts the guard covers every verb rather
// than the two somebody remembered.
func TestPreexistingEstate_OnlyIgniteMayDestroy(t *testing.T) {
	for _, verb := range knownVerbs {
		ctx := run.NewContext(t.TempDir(), "site0")
		applyDestroyPolicy(ctx, verb)

		if verb == "break-ground" {
			if ctx.PreexistingEstate {
				t.Error("ignite cannot clean up after itself; a half-finished ignition would leave VMs nothing tracks")
			}
			continue
		}
		if !ctx.PreexistingEstate {
			t.Errorf("verb %q would reach EmergencyDestroy on failure; it did not create the estate and must never tear it down", verb)
		}
	}
}

// A complete converge reported "Phases complete: render .. sterilize (9 of
// 10)", because the count was measured against the ignition sequence it is not
// part of. Nine of ten reads as a run that stopped short; it had finished.
func TestCompletionMessage_MeasuresAgainstTheRightSequence(t *testing.T) {
	if got := completionMessage(phases.AllPhases); !strings.Contains(got, "Ignition complete") {
		t.Errorf("a full ignition should say so, got %q", got)
	}
	if got := completionMessage(phases.ConvergePhases); !strings.Contains(got, "Converge complete") {
		t.Errorf("a full converge should say so rather than counting phases, got %q", got)
	}

	// A partial converge counts against the converge sequence, not the longer
	// ignition one.
	partial := phases.ConvergePhases[2:]
	got := completionMessage(partial)
	want := fmt.Sprintf("(%d of %d)", len(partial), len(phases.ConvergePhases))
	if !strings.Contains(got, want) {
		t.Errorf("partial converge should report %s, got %q", want, got)
	}
}
