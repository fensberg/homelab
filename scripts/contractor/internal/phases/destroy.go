package phases

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"homelab/contractor/internal/config"
	"homelab/contractor/internal/run"
)

// Destroy is the supported way to take an estate down.
//
// Until this existed, `tofu destroy` lived only on the failure route, inside
// EmergencyDestroy - so the only way to tear down an estate that had ignited
// successfully was to make something fail, or to drive tofu by hand and
// remember the state migration yourself. That is a bad shape for two reasons
// beyond the obvious: a staging site is only cheap if it can be thrown away,
// and the e2e test tier cannot cover the whole ignition sequence without a
// teardown it is allowed to call.
//
// # What stops a stranger running this
//
// The credentials, and they are not a formality. Destroy renders the config
// from 1Password first, exactly as a real run does. Without a 1Password
// session there is no Proxmox token, no hypervisor endpoint and no state
// database password - `tofu` has nothing to authenticate with and nothing to
// point at. Somebody with a terminal and a copy of this repository can run
// this command all day and destroy nothing.
//
// The -confirm flag is not that lock. It is the guard against a typo by
// somebody who *does* hold the credentials, which is a different and more
// likely accident.
func Destroy(ctx *run.Context, confirm string) error {
	if err := ConfirmDestroy(ctx.Site, confirm); err != nil {
		return err
	}

	run.WritePhase("Destroy", "Tear down this site's infrastructure, then wipe the workspace.")

	// Render first. This is both the credential check and the thing that
	// makes the rest possible - every step below needs the rendered config,
	// and on a workstation that has already sterilized, there is none.
	run.Info("rendering the config (this is the credential check)")
	if err := Render(ctx); err != nil {
		return fmt.Errorf("could not render the config, so there is nothing to authenticate with: %w", err)
	}

	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		return err
	}
	net, err := config.ResolveSiteNetwork(cfg, ctx.Site)
	if err != nil {
		return err
	}

	// Say which estate is about to go. An operator who is about to destroy
	// the wrong estate is usually looking at the right site key and the
	// wrong estate - the hostnames are what break that.
	//
	// This half is from the config, and says so, because it is printed before
	// the state is reachable. The config is the right source for "which
	// estate": it is what names the hypervisor and the network. It is the
	// wrong source for "how many machines", which is why the count is
	// re-stated from state further down, before anything irreversible.
	fmt.Println()
	run.Warn(fmt.Sprintf("About to destroy site %q (%s), as described by the config:", ctx.Site, net.Label))
	for _, vm := range net.AllMachineNames() {
		run.Warn("  VM        " + vm)
	}
	for _, h := range net.Hypervisors {
		run.Warn("  on        " + h.Hostname + " (" + h.IP + ")")
	}
	run.Warn("  network   " + net.SiteCIDR)
	fmt.Println()

	// Prove the hypervisor answers before handing anything to a provider.
	//
	// Before the state, before init, before anything irreversible. A provider
	// asked to read or delete a resource on an unreachable API does not fail -
	// it waits, and OpenTofu waits with it. Observed as a teardown that sat for
	// ninety minutes and, when interrupted, reported "Plugin did not respond:
	// the plugin failed to respond to the plugin6.(*GRPCProvider).ReadResource
	// call", which is what a stuck outbound call looks like from the far side.
	//
	// This matters more here than on any other path. A converge that cannot
	// reach the estate stops before it applies. A teardown that cannot reach it
	// stops partway through removing things, having already emptied the object
	// storage and forgotten resources out of state - the same explosive with
	// the fuse half burnt.
	if err := checkDestroyPreconditions(net); err != nil {
		return err
	}

	// Reach the state before trying to destroy what it describes.
	//
	// After a successful ignition there is no local state and no
	// backend_pg.tf: Migrate moved state into Postgres and Sterilize removed
	// both files. tearDown looks for exactly those two things, so a destroy
	// run from that state found nothing, skipped the teardown and reported
	// "Site destroyed" over three running VMs. Which is the only state a real
	// destroy is ever launched from.
	//
	// So if there is no local state, assume the state is where the successful
	// path put it and wire the backend up explicitly rather than hoping a
	// leftover .terraform directory still remembers.
	if _, err := os.Stat(ctx.LocalState); err != nil {
		if _, err := os.Stat(ctx.BackendPgOn); err != nil {
			run.Info("no local state - looking for it in the cluster's Postgres")
			connStr, host, port, err := buildStateConnStr(ctx)
			if err != nil {
				return err
			}
			run.Info(fmt.Sprintf("connecting to the state database at %s:%d", host, port))
			if err := attachToStateInPostgres(ctx, func() error {
				return run.Tofu(ctx, "tofu init (pg backend)",
					"init", "-input=false", "-reconfigure",
					"-backend-config=conn_str="+connStr,
				)
			}); err != nil {
				return fmt.Errorf(`could not reach the state database, so there is nothing to destroy from.

THE LIKELIEST CAUSE IS THAT THERE IS NOTHING LEFT TO DESTROY. State lives in
Postgres inside the cluster, so a teardown that already succeeded took both the
cluster and its state with it, and this run has no way to tell that apart from
a cluster that is unreachable for some other reason.

Look at the hypervisor. If the machines are gone, the estate is already down and
the workspace just needs clearing:

    task clean-secrets SITE=%s

If the machines are still there, the state that described them is genuinely
lost. Restore the age-encrypted backup from object storage before destroying
anything - see docs/state-and-secret-rotation.md.

underlying error: %w`, ctx.Site, err)
			}
		}
	} else {
		run.Info("tofu init")
		if err := run.TofuInit(ctx); err != nil {
			return err
		}
	}

	// What is actually going to be destroyed, from state, at the last moment
	// before it is.
	//
	// `tofu destroy` works from state; the banner above is built from the
	// rendered config. When the two disagree the banner under-reports, and it
	// under-reports in the reassuring direction at the exact moment somebody
	// is deciding whether to proceed with something irreversible (#93). Set
	// the count to three against a running five and the banner names three.
	//
	// Config and state disagreeing is not an edge case here - it is precisely
	// the situation a teardown is most often reached for, and the runbook's
	// own step 1 exists because of it.
	reportMachinesInState(ctx, len(net.VMNames))

	// Show the scope, then ask.
	//
	// -confirm is not consent to a scope. It runs at the very top of this
	// function, before Render, and refuses only a mismatch between two flags -
	// which makes it a guard against a typo by somebody who already holds the
	// credentials. That is a real and different property, and it is checked
	// earlier than a prompt can be, which is the right order. What it is not is
	// being shown what will go and agreeing to it (#213).
	if err := confirmDestroyScope(ctx); err != nil {
		return err
	}

	res := tearDown(ctx)
	if !res.SafeToSterilize {
		return fmt.Errorf("teardown did not complete - state and secrets have been left in place on purpose, see the messages above")
	}

	// Secrets go either way: they are on this workstation and there is no
	// reading of events in which leaving them is the safer choice.
	if err := Sterilize(ctx, false); err != nil {
		return err
	}

	// But "nothing to destroy" is not "destroyed". Reporting success here is
	// exactly the bug that printed "Site destroyed" over three running VMs,
	// and it is worse than a false alarm: it is the one message that stops
	// anybody going to look.
	if !res.Destroyed {
		return fmt.Errorf(`no state was found, so nothing was destroyed.

The workspace has been sterilized - the secrets on it are gone - but this
command cannot tell you whether the estate is still running. Check the
hypervisor by hand for %s.

If the estate is still up and its state is only in the cluster's Postgres,
restore the age-encrypted backup from object storage first: see
docs/state-and-secret-rotation.md`, vmIDHint(ctx))
	}

	return nil
}

// ConfirmDestroy requires the site to be named twice: once to select it and
// once to confirm it. Exact match only - no case folding, no trimming, no
// prefixes. Every loosening is a way for a confirmation to succeed that the
// operator did not actually type.
func ConfirmDestroy(site, confirm string) error {
	if strings.TrimSpace(site) == "" {
		return fmt.Errorf("no site selected; -site is required")
	}
	if confirm == "" {
		return fmt.Errorf(`destroy needs the site named twice. Re-run with:

    -site %s -destroy -confirm %s

This is deliberately awkward. Naming an estate twice is not something that
happens by accident, and there is no flag that skips it`, site, site)
	}
	if confirm != site {
		return fmt.Errorf(`-confirm says %q but -site says %q, so this is refused.

Those disagreeing is the single most likely way the wrong estate gets torn
down: the operator is looking at one site and thinking about another`, confirm, site)
	}
	return nil
}

// destroyPrecondition is something that must be true before a provider is
// asked to touch real infrastructure.
//
// Declared as data rather than written inline, for the reason the teardown's
// own steps are: a deleted call in a run of statements is a few green lines,
// and a deleted entry in a list is a list that no longer matches what the test
// says the teardown checks.
type destroyPrecondition struct {
	name  string
	check func(*config.SiteNetwork) error
}

// DestroyPreconditions is what must answer before the teardown begins.
//
// Before, not during. A demolish that starts and stops partway has already
// emptied the object storage and forgotten resources out of state, and left
// machines running that nothing tracks - which is worse than one that refuses
// to begin, because it is the same explosive with the fuse half burnt.
func DestroyPreconditions(probe time.Duration) []destroyPrecondition {
	return []destroyPrecondition{
		{
			name: "the hypervisor's API",
			check: func(net *config.SiteNetwork) error {
				if len(net.Hypervisors) == 0 {
					return fmt.Errorf("this site declares no hypervisor, so there is nothing to destroy against")
				}
				if run.TestPort(net.Hypervisors[0].IP, proxmoxAPIPort, probe) {
					return nil
				}
				// The address is deliberately absent: it comes from the vault
				// like every other value here, and this output gets pasted into
				// issues. It is in config/management.rendered.json, which this
				// run has just written.
				return fmt.Errorf(`cannot reach this site's hypervisor on the Proxmox API port.

Nothing has been destroyed, and nothing will be. This is checked before the
teardown starts because a provider asked to work against an unreachable API
does not fail - it waits, and everything behind it waits too. A teardown that
hangs partway through is how machines are left that nothing tracks.

The address is in config/management.rendered.json. If it is an overlay address,
check the overlay carries traffic rather than merely showing the device online:
'tailscale status' names the peer, and scripts/survey probes it`)
			},
		},
	}
}

func checkDestroyPreconditions(net *config.SiteNetwork) error {
	for _, p := range DestroyPreconditions(hypervisorProbeTimeout) {
		run.Info("checking " + p.name + " ...")
		if err := p.check(net); err != nil {
			return err
		}
		run.Ok(p.name + " answers")
	}
	return nil
}

const (
	proxmoxAPIPort = 8006

	// Long enough for a hypervisor over an overlay, short enough that a
	// teardown against an unreachable one fails in seconds rather than
	// hanging. Passed in rather than read here so a test can exercise the
	// unreachable path without spending it: a probe that cannot be exercised
	// quickly is a probe nobody exercises.
	hypervisorProbeTimeout = 10 * time.Second
)

// vmInstance matches an instance of the control-plane VM resource in
// `tofu state list` output, capturing the for_each key.
//
// The key rather than the name attribute, deliberately. Reading the name would
// mean `tofu state show` or `tofu show -json`, which print every attribute the
// provider did not mark sensitive - the same output that leaked the cluster's
// certificate authorities once already. `state list` prints addresses and
// nothing else, which is why it is on the quiet allowlist.
var vmInstance = regexp.MustCompile(`^proxmox_virtual_environment_vm\.talos_cp\["([^"]+)"\]$`)

// machinesInState returns the for_each keys of the control-plane machines
// Terraform is tracking.
func machinesInState(out string) []string {
	var keys []string
	for _, line := range strings.Split(out, "\n") {
		if m := vmInstance.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			keys = append(keys, m[1])
		}
	}
	sort.Strings(keys)
	return keys
}

// reportMachinesInState prints what the teardown will actually remove, and
// says so loudly when that is not what the config described.
//
// Deliberately non-fatal. This is the last thing printed before an operation
// the operator has already confirmed, and refusing here would strand an estate
// that is mid-teardown for a reason that is informational. Being wrong about
// the count is the thing to report; it is not a thing to stop for.
func reportMachinesInState(ctx *run.Context, fromConfig int) {
	out, err := run.CmdOutputQuiet(ctx.ClusterDir, "tofu", "state", "list")
	if err != nil {
		run.Warn("  could not read the machine list from state, so the list above is " +
			"the config's and may under-report. The teardown works from state regardless.")
		return
	}

	keys := machinesInState(out)
	if len(keys) == fromConfig {
		return
	}

	fmt.Println()
	run.Warn(fmt.Sprintf(
		"the config describes %d machine(s); state holds %d, and the teardown works from state:",
		fromConfig, len(keys)))
	for _, k := range keys {
		run.Warn("  VM        " + k)
	}
	run.Warn("  Everything above goes. The shorter list is the stale one.")
	fmt.Println()
}

// attachToStateInPostgres enables the Postgres backend, runs init against it,
// and takes the backend file back out again if that init fails.
//
// The cleanup is the whole reason this is a function rather than four inline
// lines. backend_pg.tf declares a backend for the entire module, so a copy left
// behind by a failed take-over is picked up by EVERY later `tofu init` in the
// workspace - including phases with no interest in cluster state. With no
// -backend-config alongside it, tofu falls back to dialling localhost, and a
// fresh ignition dies in its Overlay phase with
//
//	Error: dial tcp [::1]:5432: connect: connection refused
//
// which names Postgres, which is not involved, in a phase that only mints a
// tailnet key. The estate deadlocks in both directions at once: the destroy
// cannot find a cluster and the rebuild cannot start.
//
// It happened. The file is gitignored, so nothing about the working tree looked
// wrong either.
func attachToStateInPostgres(ctx *run.Context, init func() error) error {
	if err := copyFile(ctx.BackendPgOff, ctx.BackendPgOn); err != nil {
		return fmt.Errorf("enabling the Postgres backend: %w", err)
	}
	if err := init(); err != nil {
		if rmErr := os.Remove(ctx.BackendPgOn); rmErr != nil && !os.IsNotExist(rmErr) {
			return fmt.Errorf("%w (and the backend file could not be removed afterwards: %v)", err, rmErr)
		}
		return err
	}
	return nil
}

// confirmDestroyScope prints everything the teardown will remove and, where
// there is a human to ask, asks.
//
// WHAT THE LIST IS FOR. Losing the machines is recoverable - they are
// disposable and the estate is built around that. The irreversible line is the
// OBJECT STORAGE, and it is the one nothing said out loud: Cloudflare will not
// delete a bucket with objects in it, so the teardown empties it first, and the
// age-encrypted state dumps that exist specifically to survive a total loss go
// with it. Eleven objects went that way once (#94). The VM list is context
// around that one line.
//
// WHY THE PROMPT IS CONDITIONAL, and why that is not the escape hatch #213
// warns about. It is skipped when stdin is not a terminal, because the e2e tier
// tears down the estate it just built and there is nobody there to answer. The
// plan is still PRINTED in that case - it lands in the run log, which is where
// somebody reading afterwards looks. A `-yes` flag would be the thing to avoid:
// it would exist to be passed habitually, by a human, on the path where the
// question is worth asking.
//
// Only on this path. EmergencyDestroy in sterilize.go tears down what a failed
// ignition created, unattended, and a prompt there would leave orphaned
// machines waiting on somebody to answer.
func confirmDestroyScope(ctx *run.Context) error {
	fmt.Println()
	run.Warn("This teardown will remove:")

	if out, err := run.CmdOutputQuiet(ctx.ClusterDir, "tofu", "state", "list"); err == nil {
		resources := 0
		for _, line := range strings.Split(out, "\n") {
			if addr := strings.TrimSpace(line); addr != "" {
				run.Warn("  " + addr)
				resources++
			}
		}
		if resources == 0 {
			run.Warn("  (state holds no resources - there may be nothing to destroy)")
		}
	} else {
		// Reported rather than passed over. A scope that could not be read and
		// a scope that is empty must not look the same at the moment somebody
		// is deciding whether to proceed with something irreversible.
		run.Warn("  COULD NOT READ THE STATE, so this list is not the scope: " + err.Error())
	}

	reportObjectStorageAtRisk(ctx)

	fmt.Println()
	if !stdinIsATerminal() {
		run.Warn("not a terminal, so nothing was asked - the scope above is the record")
		return nil
	}

	fmt.Printf("Type the site name (%s) to proceed, or anything else to stop: ", ctx.Site)
	var answer string
	if _, err := fmt.Scanln(&answer); err != nil {
		return fmt.Errorf("nothing was typed, so the teardown is refused")
	}
	if strings.TrimSpace(answer) != ctx.Site {
		return fmt.Errorf("%q is not %q, so the teardown is refused and nothing has been touched", answer, ctx.Site)
	}
	return nil
}

// reportObjectStorageAtRisk names what the teardown destroys and what it does
// not, and says both out loud.
//
// It reports the buckets that survive as well as the one that does not,
// deliberately. An operator who has read #94, or the old version of this
// warning, believes a teardown takes the state dumps with it - and somebody
// who believes their backups are about to be destroyed makes different, worse
// decisions in the five minutes before a demolish. Naming the survivors is how
// that belief gets corrected at the only moment it matters.
func reportObjectStorageAtRisk(ctx *run.Context) {
	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		run.Warn("  object storage: could not read the rendered config, so this cannot say what is in it")
		return
	}
	site, ok := cfg.Sites[ctx.Site]
	if !ok {
		return
	}
	net, err := config.ResolveSiteNetwork(cfg, ctx.Site)
	if err != nil {
		run.Warn("  object storage: could not resolve the site, so this cannot name the buckets")
		return
	}

	for _, bucket := range config.Buckets {
		name := bucket.Name(net)

		// A bucket that survives needs no reading. Saying so is the useful
		// half of this report - an operator who believes their backups are
		// about to be destroyed makes worse decisions in the five minutes
		// before a demolish - and it costs no credential, which matters
		// because staging and production have none.
		if bucket.Keep {
			run.Info("  object storage: " + name + " SURVIVES - holds " + bucket.Holds)
			continue
		}

		cred, err := site.ObjectStorage.CredentialFor(bucket.Key)
		if err != nil || strings.TrimSpace(cred.AccessKeyID) == "" {
			// Loud rather than skipped. This bucket is about to be emptied and
			// destroyed, and not being able to say what is in it is a worse
			// answer than any number - it means the line below that normally
			// warns about contents will simply not appear.
			run.Warn("  object storage: " + name + " WILL BE DESTROYED, and no credential here can read it")
			run.Warn("  So this cannot tell you what is in it. Check it in the vendor's console before continuing.")
			continue
		}

		remote := "R2:" + name
		size, err := run.CmdOutputEnv(ctx.ClusterDir, r2Env(cfg.ObjectStorage, cred), "rclone", "--log-level", "ERROR", "size", remote)
		if err != nil {
			// Not alarming on its own: a bucket that was never created because
			// an earlier run failed reads exactly like this.
			run.Warn("  object storage: " + name + " - could not be read, so this cannot say what is in it")
			continue
		}
		summary := strings.Join(strings.Fields(strings.ReplaceAll(size, "\n", " ")), " ")

		if strings.Contains(summary, "Total objects: 0") {
			run.Warn("  object storage: " + name + " is empty")
			continue
		}
		run.Warn("  object storage: " + name + " - " + summary)
		run.Warn("  THIS HOLDS " + strings.ToUpper(bucket.Holds) + ", AND THE TEARDOWN DESTROYS IT.")
		run.Warn("  Cloudflare will not delete a bucket with objects in it, so the teardown")
		run.Warn("  empties it first. The age-encrypted state dumps are NOT in here - they")
		run.Warn("  have a bucket of their own that this operation leaves alone (#94).")
	}
}

// stdinIsATerminal reports whether there is a human to ask.
//
// os.Stat rather than a dependency: every program under scripts/ is
// dependency-free, and this is one bit of information.
func stdinIsATerminal() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
