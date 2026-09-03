package phases

import (
	"reflect"
	"strings"
	"testing"
)

// The tailnet key's expiry was cut from 90 days to an hour, which is only safe
// if every run mints a genuinely new one. tofu will not do that on its own: an
// expired key is still a perfectly current resource as far as state is
// concerned, so a plain apply reports no changes and the playbook is handed a
// credential Tailscale has already retired. -replace is the documented way to
// say "recreate this one thing", and it is exactly the situation the flag is
// for.

func TestOverlayApplyArgs_ForcesReplacementWhenTheKeyIsInState(t *testing.T) {
	got := overlayApplyArgs(true)
	want := []string{
		"apply", "-input=false", "-auto-approve", "-json",
		"-var", "overlay_key_wanted=true",
		"-replace=" + overlayKeyAddress,
		"-target=" + overlayKeyAddress,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

// On the first run of a brand new estate the resource does not exist yet.
// `-replace` on an address that is not in state is refused by tofu, so asking
// for it would turn a working first ignition into an error.
func TestOverlayApplyArgs_OmitsReplaceOnAFirstRun(t *testing.T) {
	got := overlayApplyArgs(false)
	want := []string{
		"apply", "-input=false", "-auto-approve", "-json",
		"-var", "overlay_key_wanted=true",
		"-target=" + overlayKeyAddress,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestStateListContains(t *testing.T) {
	list := "proxmox_virtual_environment_vm.talos_cp[0]\ntailscale_tailnet_key.hypervisor\n"
	if !stateListContains(list, "tailscale_tailnet_key.hypervisor") {
		t.Error("should have found the key")
	}
	if stateListContains(list, "cloudflare_r2_bucket.homelab") {
		t.Error("should not have found a resource that is not there")
	}
	// A prefix match would say yes to talos_cp[0] when asked about talos_cp,
	// and the two are different addresses.
	if stateListContains(list, "proxmox_virtual_environment_vm.talos_cp") {
		t.Error("matching must be on the whole address, not a prefix")
	}
}

// The Overlay phase is the only thing that should ask for the key to exist.
//
// Every other apply in a run leaves var.overlay_key_wanted at its default of
// false, which is what makes the untargeted apply at the end of the Cluster
// phase revoke the key rather than reconcile it. Without the flag here the
// key is never created at all and the playbook gets an empty auth key; with
// it set anywhere else, the churn #138 describes comes back.
func TestOverlayIsTheOnlyPhaseThatAsksForTheKey(t *testing.T) {
	args := overlayApplyArgs(false)

	var asked bool
	for i, a := range args {
		if a == "-var" && i+1 < len(args) && args[i+1] == "overlay_key_wanted=true" {
			asked = true
		}
	}
	if !asked {
		t.Fatal("the overlay apply does not set overlay_key_wanted=true, so the key " +
			"is never created and the hypervisor playbook is handed an empty auth key")
	}
}

// The address carries an index because the resource is conditional. -target
// and -replace both need the instance; the bare resource address matches
// nothing once `count` is present, and tofu treats that as "no changes" rather
// than as an error - so the phase would report success having minted nothing.
func TestTheKeyAddressIsAnInstanceAddress(t *testing.T) {
	if !strings.HasSuffix(overlayKeyAddress, "[0]") {
		t.Errorf("overlayKeyAddress is %q, which is a resource address rather than an "+
			"instance address. The key is conditional on var.overlay_key_wanted, so "+
			"`count` indexes it even at one - and -target against the bare address "+
			"silently matches nothing.", overlayKeyAddress)
	}
}
