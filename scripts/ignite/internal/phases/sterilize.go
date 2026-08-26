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
//
// Returns whether it is safe to sterilize afterward. A destroy that actually
// fails (state existed, tofu errored - e.g. a state lock still held by a
// just-interrupted apply that has not yet exited) must NOT be followed by
// sterilize: wiping the state afterward would destroy the only path back to
// retrying the destroy or diagnosing what's left running. Missing state
// entirely is not a failure of this function - there is nothing to destroy,
// and sterilizing whatever secrets remain is still correct.
func EmergencyDestroy(ctx *run.Context) bool {
	run.Warn("Run did not complete. Tearing down so nothing is left orphaned.")

	if _, err := os.Stat(ctx.LocalState); err != nil {
		run.Warn("No local state file - nothing for tofu to destroy.")
		run.Warn("Check Proxmox by hand for VMs 100-102.")
		return true
	}

	if err := run.Cmd(ctx.ClusterDir, "tofu", "destroy", "-input=false", "-auto-approve"); err != nil {
		run.Warn("tofu destroy failed. State and secrets are being left in place - sterilizing now would destroy your only way to retry the destroy or diagnose what's left running.")
		run.Warn("Check Proxmox manually for VMs 100-102, then either re-run 'tofu destroy' in management/cluster yourself or run 'task clean-secrets' once you've confirmed nothing is orphaned.")
		return false
	}
	run.Ok("infrastructure destroyed cleanly")
	return true
}
