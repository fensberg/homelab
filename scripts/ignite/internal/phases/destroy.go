package phases

import (
	"fmt"
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

	run.Info("tofu init")
	if err := run.TofuInit(ctx); err != nil {
		return err
	}

	if ok := tearDown(ctx); !ok {
		return fmt.Errorf("teardown did not complete - state and secrets have been left in place on purpose, see the messages above")
	}

	return Sterilize(ctx, false)
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
