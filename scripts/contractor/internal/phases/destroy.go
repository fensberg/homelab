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
	for _, vm := range append(append([]string{}, net.VMNames...), net.WorkerNames...) {
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
			if err := copyFile(ctx.BackendPgOff, ctx.BackendPgOn); err != nil {
				return fmt.Errorf("enabling the Postgres backend: %w", err)
			}
			connStr, host, port, err := buildStateConnStr(ctx)
			if err != nil {
				return err
			}
			run.Info(fmt.Sprintf("connecting to the state database at %s:%d", host, port))
			if err := run.Tofu(ctx, "tofu init (pg backend)",
				"init", "-input=false", "-reconfigure",
				"-backend-config=conn_str="+connStr,
			); err != nil {
				return fmt.Errorf(`could not reach the state database, so there is nothing to destroy from.

State lives in Postgres inside the cluster after a successful ignition. If that
cluster is already gone, the state went with it - restore the age-encrypted
backup from object storage first (see docs/state-and-secret-rotation.md), or
remove whatever is left in Proxmox by hand.

underlying error: %w`, err)
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
