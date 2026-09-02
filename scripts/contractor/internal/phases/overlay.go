package phases

import (
	"fmt"
	"os"
	"strings"

	"homelab/contractor/internal/run"
)

// Overlay mints a tagged auth key for the hypervisor to join the overlay
// network.
func Overlay(ctx *run.Context) error {
	run.WritePhase("Overlay", "Mint a tagged auth key for the hypervisor to join the overlay network.")

	run.Info("tofu init")
	if err := run.TofuInit(ctx); err != nil {
		return err
	}

	// Applied ahead of the playbook so the hypervisor can log in with a
	// tagged key. The tag is what makes autoApprovers approve the subnet
	// route without anyone touching the admin console.
	//
	// Replaced rather than merely applied, whenever it already exists. The key
	// now expires in an hour, and tofu has no way to know that: an expired key
	// is still a current resource in state, so a plain apply reports no changes
	// and hands the playbook a credential Tailscale has already retired.
	run.Info("minting a tagged auth key")
	list, _ := run.CmdOutputQuiet(ctx.ClusterDir, "tofu", "state", "list")
	args := overlayApplyArgs(stateListContains(list, overlayKeyAddress))
	if err := run.TofuApplyArgs(ctx, "tofu apply (overlay network)", args...); err != nil {
		return err
	}

	key, err := run.TofuOutputRaw(ctx, "overlay_network_auth_key")
	if err != nil {
		return err
	}
	tag, err := run.TofuOutputRaw(ctx, "overlay_router_tag")
	if err != nil {
		return err
	}

	// Handed to Ansible as a vars file rather than on the command line,
	// where it would be visible in the process list. Sterilize deletes it.
	run.Info("writing the auth key for Ansible")
	content := fmt.Sprintf("---\noverlay_auth_key: %q\noverlay_router_tag: %q\n", key, tag)
	if err := os.WriteFile(ctx.OverlayVars, []byte(content), 0o600); err != nil {
		return err
	}

	run.Ok("auth key minted; the tailnet policy auto-approves this subnet")
	return nil
}

// overlayKeyAddress is the one resource this phase owns.
const overlayKeyAddress = "tailscale_tailnet_key.hypervisor"

// overlayApplyArgs builds the apply. Split out from the phase so the decision
// that matters - whether to ask for a replacement - is testable without a
// hypervisor, a tailnet or a state file.
//
// -replace is only added when the address is already in state. tofu refuses it
// otherwise, which would turn the first run of a brand new estate into an
// error.
//
// -json is not optional. Without it the apply streams every non-sensitive
// attribute it touches, and this resource's description is built from the
// site's name - which is how a vault value reached a public Actions log.
func overlayApplyArgs(keyInState bool) []string {
	args := []string{"apply", "-input=false", "-auto-approve", "-json"}
	if keyInState {
		args = append(args, "-replace="+overlayKeyAddress)
	}
	return append(args, "-target="+overlayKeyAddress)
}

// stateListContains matches whole addresses. A prefix match would answer yes
// for talos_cp when asked about talos_cp[0], and those are different objects.
func stateListContains(stateList, address string) bool {
	for _, line := range strings.Split(stateList, "\n") {
		if strings.TrimSpace(line) == address {
			return true
		}
	}
	return false
}
