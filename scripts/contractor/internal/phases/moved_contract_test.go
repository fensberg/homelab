package phases

import (
	"os"
	"strings"
	"testing"
)

// The converge settles a pending rename BEFORE its first targeted apply.
//
// WHAT THIS GUARDS. OpenTofu refuses to build a targeted plan while a `moved`
// block is pending - it names both endpoints and stops with "Moved resource
// instances excluded by targeting". Every apply in the converge up to the
// Cluster phase is targeted, so one rename anywhere in the configuration halts
// the converge at its very first apply.
//
// It happened. Renaming a single R2 bucket resource stopped every converge on
// main: the buckets the repository had begun describing were never created, so
// the nightly backup failed with NoSuchBucket and the estate lost its off-site
// state again; and the Talos machine configuration stopped being applied, so a
// metrics listener the repository declared stayed shut and Alertmanager paged
// about it every four hours for a day.
//
// Nothing caught it before the merge, and that is the part worth remembering:
// `contractor plan` runs ONE UNTARGETED plan, and an untargeted plan is exactly
// the shape that cannot see this. The pre-merge check and the post-merge action
// used different tofu invocations, so the check was structurally incapable of
// failing the way the action would. The pull request was green.
//
// This is the hermetic half of the answer - it needs no estate, so it runs on
// every pull request. The ordering is the whole property: settling after the
// first apply is the same as not settling at all.
func TestComputeSettlesRenamesBeforeItApplies(t *testing.T) {
	body, err := os.ReadFile("compute.go")
	if err != nil {
		t.Fatalf("reading compute.go: %v", err)
	}
	src := string(body)

	settle := strings.Index(src, "run.SettleMoves(ctx)")
	if settle < 0 {
		t.Fatal(`compute.go does not call run.SettleMoves.

Without it, one ` + "`moved`" + ` block anywhere in management/cluster stops every
converge at the first targeted apply, and nothing downstream runs - not the
VMs, not the machine configuration, not the untargeted apply at the end of
Cluster that would have settled the move itself.`)
	}

	apply := strings.Index(src, "run.TofuApply(")
	if apply < 0 {
		t.Fatal(`compute.go no longer calls run.TofuApply.

If the phase was restructured, this contract needs re-examining rather than
re-pointing: something still has to settle a pending rename before the first
targeted apply, wherever that apply now lives.`)
	}

	if settle > apply {
		t.Errorf(`compute.go settles renames at byte %d, after its first targeted apply at byte %d.

Settling after the first targeted apply is the same as not settling at all: the
apply is what gets refused, so it never reaches the line that would have fixed
it.`, settle, apply)
	}
}
