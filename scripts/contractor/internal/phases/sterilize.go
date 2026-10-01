package phases

import (
	"fmt"
	"homelab/details/tcp"
	"os"
	"time"

	"homelab/contractor/config"
	"homelab/contractor/internal/run"
)

// Sterilize removes every secret and the local state file from this
// workstation. Safe to run twice - missing targets are silently skipped.
func Sterilize(ctx *run.Context, quiet bool) error {
	if !quiet {
		run.WritePhase("Sterilize", "Remove every secret and the local state file from this workstation.")
	}

	targets := sterilizeTargets(ctx)
	for _, t := range targets {
		if err := run.RemoveIfExists(t); err != nil {
			return err
		}
	}
	// The Record phase's workspace, a directory the phase owns outright. What
	// it holds is scrubbed, but a run that dies partway leaves a throwaway CA
	// and a half-scrubbed record in it.
	if err := run.RemoveTreeIfExists(ctx.AsBuiltDir); err != nil {
		return err
	}
	run.Ok("workspace sterilized")
	return nil
}

// sterilizeTargets is the list of everything a run may leave behind.
//
// Split out so it can be asserted on. A file missing from this list is a
// secret left on disk or a workspace that no longer validates, and neither
// failure announces itself - the run still reports success.
func sterilizeTargets(ctx *run.Context) []string {
	targets := []string{
		ctx.ConfigRendered,
		ctx.InventoryOut,
		ctx.OverlayVars,
		ctx.SiteVars,
		ctx.Kubeconfig,
	}
	for _, root := range ctx.Roots() {
		targets = append(targets, rootTargets(root)...)
	}
	return targets
}

// rootTargets is what a run may leave in one root. Every root, the same list:
// a root this missed would keep its state and its saved plan on disk.
func rootTargets(root run.Root) []string {
	return []string{
		root.BackendPgOn,
		root.LocalState,
		root.LocalState + ".backup",

		// tofu's record of which backend is configured - not state, but it
		// remembers that the last one was encrypted Postgres. Left behind, the
		// next `tofu init -backend=false` fails with "Unsupported state file
		// format: this state file is encrypted", which breaks `task validate`
		// and `task test` on any machine that has completed a run. That is
		// every machine that matters, and the pre-push hook runs both.
		//
		// Safe to delete: take-over, destroy and sterilize all pass
		// -backend-config explicitly with -reconfigure, so nothing relies on
		// the cached record.
		root.BackendRecord,

		// A saved plan is a file of resource attributes. Listed here for the
		// run that fails before the Plan phase removes it itself.
		root.PlanFile,
	}
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
	return tearDown(ctx).SafeToSterilize
}

// teardownResult separates two questions that a single bool used to conflate,
// and the conflation printed "Site destroyed" over three running VMs.
//
// "Is it safe to wipe the workspace" and "was anything actually destroyed" are
// different, and the case that separates them is the one that matters: no
// state file at all. There is nothing to destroy and nothing to lose by
// sterilizing, so SafeToSterilize is true - but nothing was verified gone
// either, and a deliberate teardown must not report success from an absence.
type teardownResult struct {
	SafeToSterilize bool
	Destroyed       bool
}

// tearDown is the teardown itself, shared by the failure route
// (EmergencyDestroy, above) and the deliberate one (Destroy, in destroy.go).
// One implementation on purpose: the ordering below - migrate state out of
// the cluster before destroying the cluster, and never sterilize after a
// destroy that failed - was learned the hard way once, and a second copy of
// it is a second place to get it wrong.
func tearDown(ctx *run.Context) teardownResult {
	if _, err := os.Stat(ctx.BackendPgOn); err == nil {
		run.Warn("State lives in Postgres, inside the cluster this destroy is about to tear down - migrating it back to local first. Destroying in dependency order kills the database hosting this state before the VMs themselves are reached, which would otherwise strand the destroy partway through with no way to record what it had already done.")
		if err := demigrateStateToLocal(ctx); err != nil {
			run.Warn("Could not migrate state back to local: " + err.Error())
			run.Warn("The standalone age-encrypted state backup in object storage is the documented fallback for exactly this (see docs/epochs/01-ignition.md). Restoring it needs the state-backup identity from 1Password (" + BackupIdentityRef + "), fetched by a human, then a local 'tofu init -migrate-state' back to the local backend before destroy can proceed.")
			return teardownResult{}
		}
		run.Ok("state migrated back to local")
	}

	if _, err := os.Stat(ctx.LocalState); err != nil {
		run.Warn("No local state file - nothing for tofu to destroy.")
		run.Warn("Check Proxmox by hand for " + vmIDHint(ctx) + ".")
		return teardownResult{SafeToSterilize: true}
	}

	// Only the cluster root is destroyed. What the platform root created
	// lives inside the machines this is about to delete and goes with their
	// disks; asking a dying cluster to delete its own namespaces first hung
	// on Flux's finalizers until the destroy gave up having destroyed
	// nothing. That used to be avoided by forgetting those resources out of
	// the one state. Now they are in a root this never destroys, and
	// tests/go/repo refuses a kubernetes resource in the cluster root. The
	// platform's own state is in the database being deleted, and whatever of
	// it is on this disk is Sterilize's.
	//
	// One thing tofu cannot do for itself, learned by watching a real
	// teardown of a real estate destroy nothing at all. See teardown.go.
	emptyObjectStorage(ctx)

	if err := run.TofuDestroy(ctx, "tofu destroy"); err != nil {
		// Say what went wrong before saying what it means. This used to
		// discard err entirely and print only the advice, so a real teardown
		// failed on five VMs and reported nothing about why - leaving the
		// operator to guess at a destroy that had already stopped the
		// machines. The diagnostics tofu produced were collected and thrown
		// away one line above where they were needed.
		run.Warn("tofu destroy failed: " + err.Error())
		run.Warn("State and secrets are being left in place - sterilizing now would destroy your only way to retry the destroy or diagnose what's left running.")
		run.Warn("Check Proxmox manually for " + vmIDHint(ctx) + ", then either re-run 'tofu destroy' in management/cluster yourself or run 'task clean-secrets' once you've confirmed nothing is orphaned.")
		return teardownResult{}
	}
	run.Ok("infrastructure destroyed cleanly")
	return teardownResult{SafeToSterilize: true, Destroyed: true}
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
	if !tcp.Await(tcp.Addr(host, port), 10*time.Second, 2*time.Second) {
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

// The break-glass path below rebuilds the state database's connection string
// from first principles, which means restating values that variables.tf also
// declares. They are named here rather than written inline so
// phases/contract_test.go can assert they still match the OpenTofu source -
// turning a duplication the comment below merely acknowledges into one a test
// actually enforces.
const (
	// variables.tf: local.state_db_name / state_db_owner. Where the database
	// answers - host and port - is the address plan's, read through
	// config.ResolveSiteNetwork.
	stateDBName  = "tofu_state"
	stateDBOwner = "tofu"
)

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
	net, err := config.ResolveSiteNetwork(cfg, ctx.Site)
	if err != nil {
		return "", "", 0, err
	}

	// Where the state database answers is the address plan's.
	host, port = net.StateDatabase.Host, net.StateDatabase.Port
	connStr = fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=require",
		stateDBOwner, site.Database.Password, host, port, stateDBName)

	return connStr, host, port, nil
}

// vmIDHint names the VM ids this site would have used, so an operator cleaning
// up by hand knows what to look for. Ids are banded by octet - variables.tf
// computes them as octet*1000 + 100 + i - so the hardcoded "100-102" this replaced
// was wrong for every site including the only one that exists.
func vmIDHint(ctx *run.Context) string {
	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		return "this site's VMs"
	}
	net, err := config.ResolveSiteNetwork(cfg, ctx.Site)
	if err != nil || len(net.ControlPlaneVMIDs) == 0 {
		return "this site's VMs"
	}
	ids := net.ControlPlaneVMIDs
	return fmt.Sprintf("VMs %d-%d and the template at %d", ids[0], ids[len(ids)-1], net.TemplateVMID)
}
