package main

import (
	"slices"
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
	got, err := selectPhases("", "", false)
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
	got, err := selectPhases("", "", false)
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
	got, err := selectPhases("compute", "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "compute" {
		t.Errorf("got %v, want exactly [compute]", got)
	}
}

func TestSelectPhases_FromIsInclusiveAndRunsToTheEnd(t *testing.T) {
	got, err := selectPhases("", "migrate", false)
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
	got, err := selectPhases("", phases.AllPhases[0], false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(phases.AllPhases) {
		t.Errorf("got %d phases, want all %d", len(got), len(phases.AllPhases))
	}
}

func TestSelectPhases_FromTheLastPhaseIsJustThatPhase(t *testing.T) {
	last := phases.AllPhases[len(phases.AllPhases)-1]
	got, err := selectPhases("", last, false)
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
	got, err := selectPhases("verify", "render", false)
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
		_, err := selectPhases(tc.phase, tc.from, false)
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
	if _, err := selectPhases("Compute", "", false); err == nil {
		t.Error("expected -phase Compute to be rejected; the documented names are lower-case")
	}
}

// -destroy is not a phase, and must not look like one. A command reading
// "destroy, but only the verify phase" would be understood by somebody as a
// safe thing to run, so the combination is refused rather than interpreted.
func TestStandaloneFlagsOK(t *testing.T) {
	if err := standaloneFlagsOK(modes{Destroy: true}, "", ""); err != nil {
		t.Errorf("plain -destroy should be accepted, got: %v", err)
	}
	if err := standaloneFlagsOK(modes{}, "verify", "render"); err != nil {
		t.Errorf("with no standalone mode the phase selectors are none of this function's business, got: %v", err)
	}
	for _, tc := range []struct{ phase, from string }{
		{"verify", ""},
		{"", "render"},
		{"compute", "render"},
	} {
		if err := standaloneFlagsOK(modes{Destroy: true}, tc.phase, tc.from); err == nil {
			t.Errorf("(-destroy, %q, %q) was accepted; -destroy must not compose with the phase selectors", tc.phase, tc.from)
		}
	}
}

// -restore is the same shape as -destroy: it is not a step in the ignition
// sequence, so composing it with a phase selector describes something that
// does not exist.
func TestStandaloneFlagsOK_RestoreDoesNotComposeWithPhases(t *testing.T) {
	if err := standaloneFlagsOK(modes{Restore: true}, "", ""); err != nil {
		t.Errorf("plain -restore should be accepted, got: %v", err)
	}
	if err := standaloneFlagsOK(modes{Restore: true}, "backup", ""); err == nil {
		t.Error("-restore -phase backup was accepted")
	}
}

// The one that would actually hurt. -destroy and -restore in the same
// invocation is a genuine ambiguity - both reach real infrastructure, and the
// order they would run in is not something anyone should have to guess.
func TestStandaloneFlagsOK_ModesAreMutuallyExclusive(t *testing.T) {
	err := standaloneFlagsOK(modes{Destroy: true, Restore: true}, "", "")
	if err == nil {
		t.Fatal("-destroy and -restore together were accepted")
	}
	for _, want := range []string{"-destroy", "-restore"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should name both flags, %q missing from: %v", want, err)
		}
	}
	if err := standaloneFlagsOK(modes{Restore: true, Kubeconfig: true}, "", ""); err == nil {
		t.Error("-restore and -kubeconfig together were accepted")
	}
}

// "Ignition complete. The cluster is now self-sustaining." used to print after
// every successful invocation, including `-phase render`. That is a claim
// about a cluster made by a command that only decrypted a JSON file, and it is
// the kind of false summary that stops someone checking - the same failure
// mode as reporting "Site destroyed" over three running VMs.
func TestCompletionMessage_FullSequence(t *testing.T) {
	got := completionMessage(phases.AllPhases)
	if !strings.Contains(got, "self-sustaining") {
		t.Errorf("a full run should report ignition complete, got %q", got)
	}
}

func TestCompletionMessage_SinglePhaseDoesNotClaimACluster(t *testing.T) {
	got := completionMessage([]string{"render"})
	if strings.Contains(got, "self-sustaining") || strings.Contains(got, "Ignition complete") {
		t.Errorf("a single phase must not claim the cluster is up, got %q", got)
	}
	if !strings.Contains(got, "render") {
		t.Errorf("the message should name what actually ran, got %q", got)
	}
}

// -from compute stops at sterilize, so it does finish the sequence - but it
// never created the cluster this run's message would be describing. The
// deciding question is whether the run started at the beginning, not whether
// it reached the end.
func TestCompletionMessage_PartialRunEndingAtTheLastPhase(t *testing.T) {
	got := completionMessage([]string{"migrate", "backup", "sterilize"})
	if strings.Contains(got, "self-sustaining") {
		t.Errorf("a run that did not start at the first phase must not claim ignition, got %q", got)
	}
	for _, want := range []string{"migrate", "sterilize"} {
		if !strings.Contains(got, want) {
			t.Errorf("the message should name the range that ran, %q missing from %q", want, got)
		}
	}
}

// A converge indexes a different sequence, and the differences are the whole
// safety story: it attaches to state that already exists, and it must never
// run migrate, whose -force-copy would overwrite that state with whatever this
// workspace happened to hold.
func TestSelectPhases_ConvergeNeverMigrates(t *testing.T) {
	got, err := selectPhases("", "", true)
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
	if _, err := selectPhases("attach", "", false); err == nil {
		t.Error("ignition accepted -phase attach, which only exists in a converge")
	}
	if _, err := selectPhases("migrate", "", true); err == nil {
		t.Error("converge accepted -phase migrate, which would overwrite the estate's state")
	}
}

// Ignition ends by sterilizing, and so must a converge: the workstation should
// hold no state and no secrets afterwards either way.
func TestSelectPhases_ConvergeStillSterilizes(t *testing.T) {
	got, err := selectPhases("", "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got[len(got)-1] != "sterilize" {
		t.Fatalf("a converge must end sterilized, got %q", got[len(got)-1])
	}
}
