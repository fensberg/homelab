package phases

import (
	"fmt"
	"os"

	"homelab/ignite/internal/run"
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
	run.Info("minting a tagged auth key")
	if err := run.TofuApply(ctx, "tofu apply (overlay network)", "tailscale_tailnet_key.hypervisor"); err != nil {
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
