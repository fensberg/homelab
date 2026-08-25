package phases

import (
	"os"

	"homelab/ignite/internal/run"
)

// Sterilize removes every secret and the local state file from this
// workstation. Safe to run twice - missing targets are silently skipped.
func Sterilize(ctx *run.Context, quiet bool) error {
	if !quiet {
		run.WritePhase("Sterilize", "Remove every secret and the local state file from this workstation.")
	}

	targets := []string{
		ctx.ConfigRendered,
		ctx.InventoryOut,
		ctx.OverlayVars,
		ctx.SiteVars,
		ctx.BackendPgOn,
		ctx.LocalState,
		ctx.LocalState + ".backup",
	}
	for _, t := range targets {
		if err := run.RemoveIfExists(t); err != nil {
			return err
		}
	}
	run.Ok("workspace sterilized")
	return nil
}

// EmergencyDestroy tears down infrastructure a failed run may have created,
// before Sterilize deletes the state that describes it. Deleting state
// without destroying first would leave VMs running that nothing tracks.
func EmergencyDestroy(ctx *run.Context) {
	run.Warn("Run did not complete. Tearing down so nothing is left orphaned.")

	if _, err := os.Stat(ctx.LocalState); err != nil {
		run.Warn("No local state file - nothing for tofu to destroy.")
		run.Warn("Check Proxmox by hand for VMs 100-102.")
		return
	}

	if err := run.Cmd(ctx.ClusterDir, "tofu", "destroy", "-input=false", "-auto-approve"); err != nil {
		run.Warn("tofu destroy failed. Check Proxmox manually for VMs 100-102 before re-running.")
		return
	}
	run.Ok("infrastructure destroyed cleanly")
}
