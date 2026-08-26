package phases

import (
	"fmt"
	"os"
	"time"

	"homelab/ignite/internal/config"
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

	if _, err := os.Stat(ctx.BackendPgOn); err == nil {
		run.Warn("State lives in Postgres, inside the cluster this destroy is about to tear down - migrating it back to local first. Destroying in dependency order kills the database hosting this state before the VMs themselves are reached, which would otherwise strand the destroy partway through with no way to record what it had already done.")
		if err := demigrateStateToLocal(ctx); err != nil {
			run.Warn("Could not migrate state back to local: " + err.Error())
			run.Warn("The standalone age-encrypted state backup in object storage is the documented fallback for exactly this (see docs/epochs/01-ignition.md). Restoring it needs backup_identity from 1Password, fetched by a human, then a local 'tofu init -migrate-state' back to the local backend before destroy can proceed.")
			return false
		}
		run.Ok("state migrated back to local")
	}

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

// demigrateStateToLocal reverses Migrate: state comes back out of Postgres
// and onto local disk before EmergencyDestroy touches anything, so the
// actual destroy operates on a state file immune to whatever happens to the
// cluster hosting the database partway through.
func demigrateStateToLocal(ctx *run.Context) error {
	connStr, host, port, err := buildStateConnStr(ctx)
	if err != nil {
		return err
	}

	run.Info(fmt.Sprintf("checking Postgres at %s:%d is still reachable ...", host, port))
	if !run.WaitForPort(host, port, 10*time.Second, 2*time.Second) {
		return fmt.Errorf("postgres at %s:%d is not reachable - it may already be gone", host, port)
	}

	if err := os.Remove(ctx.BackendPgOn); err != nil {
		return err
	}

	return run.Tofu(ctx, "tofu init -migrate-state (pg -> local)",
		"init", "-input=false", "-migrate-state", "-force-copy",
		"-backend-config=conn_str="+connStr,
	)
}

// buildStateConnStr reconstructs the pg backend's connection string from the
// rendered config plus the same fixed locals database.tf itself uses
// (state_db_nodeport/name/owner - see variables.tf), rather than reading it
// back as a Terraform output. That is the normal, documented way (Migrate
// does exactly that), but it requires the backend already initialized and
// reachable - exactly what cannot be assumed here, since the whole point of
// this function is to run before that assumption might stop being true.
// Duplicating a handful of stable locals here is a deliberate, small
// trade-off for a break-glass path that has to work even when Terraform
// itself cannot currently reach its own state.
func buildStateConnStr(ctx *run.Context) (connStr, host string, port int, err error) {
	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		return "", "", 0, fmt.Errorf("could not load rendered config: %w", err)
	}
	site, ok := cfg.Sites[ctx.Site]
	if !ok {
		return "", "", 0, fmt.Errorf("site %q not found in rendered config", ctx.Site)
	}

	// Mirrors variables.tf's node_cidr ("10.<octet>.10.0/24") and its first
	// host (100 + 0), and database.tf's state_db_nodeport/name/owner locals.
	const (
		nodePort = 30432
		dbName   = "tofu_state"
		dbOwner  = "tofu"
	)
	host = fmt.Sprintf("10.%d.10.100", site.Octet)
	connStr = fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=require",
		dbOwner, site.State.DBPassword, host, nodePort, dbName)

	return connStr, host, nodePort, nil
}
