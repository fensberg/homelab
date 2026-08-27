package phases

import (
	"fmt"
	"os"
	"strings"

	"homelab/ignite/internal/config"
	"homelab/ignite/internal/run"
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

	// Say what is about to go, by name. An operator who is about to destroy
	// the wrong estate is usually looking at the right site key and the
	// wrong estate - the hostnames are what break that.
	fmt.Println()
	run.Warn(fmt.Sprintf("About to destroy site %q (%s):", ctx.Site, net.Label))
	for _, vm := range net.VMNames {
		run.Warn("  VM        " + vm)
	}
	for _, h := range net.Hypervisors {
		run.Warn("  on        " + h.Hostname + " (" + h.IP + ")")
	}
	run.Warn("  network   " + net.SiteCIDR)
	fmt.Println()

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
