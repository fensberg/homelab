package phases

import (
	"reflect"
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
		"apply", "-input=false", "-auto-approve",
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
		"apply", "-input=false", "-auto-approve",
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
