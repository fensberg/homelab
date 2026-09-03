package phases

import (
	"strings"
	"testing"
)

// ConfirmDestroy is the only thing standing between a typo and a destroyed
// estate, so it is worth more tests than it has lines. The rule it enforces
// is deliberately awkward: naming the site twice, once to select it and once
// to confirm it, is not something that happens by accident.

func TestConfirmDestroy_MatchingNamesPass(t *testing.T) {
	if err := ConfirmDestroy("site0", "site0"); err != nil {
		t.Errorf("unexpected error for matching names: %v", err)
	}
}

func TestConfirmDestroy_EmptyConfirmationIsRefused(t *testing.T) {
	err := ConfirmDestroy("site0", "")
	if err == nil {
		t.Fatal("expected an error when -confirm is absent")
	}
	// The message has to show the exact command, or the natural next move is
	// to go looking for a flag that skips the check.
	if !strings.Contains(err.Error(), "-confirm site0") {
		t.Errorf("the error should show the exact flag to pass, got: %v", err)
	}
}

func TestConfirmDestroy_MismatchIsRefused(t *testing.T) {
	err := ConfirmDestroy("site0", "site1")
	if err == nil {
		t.Fatal("expected an error when -confirm names a different site")
	}
	// Both names must appear: the whole point of this failure is that the
	// operator is looking at one site and thinking about another.
	for _, want := range []string{"site0", "site1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should name both sites, %q is missing from: %v", want, err)
		}
	}
}

// Not case-insensitive, not whitespace-trimmed, not a prefix match. Every
// loosening here is a way for a confirmation to succeed that the operator did
// not actually type.
func TestConfirmDestroy_IsExact(t *testing.T) {
	for _, confirm := range []string{"SITE0", "Site0", " site0", "site0 ", "site", "site00"} {
		if err := ConfirmDestroy("site0", confirm); err == nil {
			t.Errorf("ConfirmDestroy(\"site0\", %q) passed; the match must be exact", confirm)
		}
	}
}

// A site named "" would make an empty -confirm succeed, which would turn the
// guard off entirely for whoever managed to reach that state.
func TestConfirmDestroy_EmptySiteIsRefusedEvenWhenConfirmMatches(t *testing.T) {
	if err := ConfirmDestroy("", ""); err == nil {
		t.Fatal("expected an error for an empty site name; an empty confirmation must never satisfy the guard")
	}
}

// The teardown works from state; the banner used to be built from the rendered
// config. When the two disagree the banner under-reports, and it under-reports
// in the reassuring direction at the moment somebody is deciding whether to
// proceed with something irreversible (#93).
//
// Parsing is tested here rather than the printing, because the parsing is the
// part that can be wrong in a way nobody sees: a pattern that matches nothing
// reports zero machines, which reads exactly like an estate that is already
// gone.
func TestMachinesInStateReadsTheControlPlaneInstances(t *testing.T) {
	const out = `data.talos_cluster_health.this
proxmox_virtual_environment_download_file.talos_image["node0"]
proxmox_virtual_environment_vm.talos_template["node0"]
proxmox_virtual_environment_vm.talos_cp["node2"]
proxmox_virtual_environment_vm.talos_cp["node0"]
proxmox_virtual_environment_vm.talos_cp["node1"]
talos_machine_secrets.this
tailscale_tailnet_key.hypervisor`

	got := machinesInState(out)
	want := []string{"node0", "node1", "node2"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v (sorted)", got, want)
		}
	}
}

// The template is a VM too, and counting it would overstate by one on every
// estate. Overstating is the safer direction but it is still wrong, and a
// banner nobody trusts is a banner nobody reads.
func TestMachinesInStateIgnoresTheTemplateAndEverythingElse(t *testing.T) {
	const out = `proxmox_virtual_environment_vm.talos_template["node0"]
proxmox_virtual_environment_file.cloud_init["node0"]
module.something.proxmox_virtual_environment_vm.talos_cp["node9"]`

	if got := machinesInState(out); len(got) != 0 {
		t.Errorf("got %v, want none - the template, an unrelated resource and a "+
			"module-nested address are all not this cluster's control plane", got)
	}
}

func TestMachinesInStateOnEmptyStateReportsNone(t *testing.T) {
	if got := machinesInState(""); len(got) != 0 {
		t.Errorf("got %v from empty output, want none", got)
	}
}
