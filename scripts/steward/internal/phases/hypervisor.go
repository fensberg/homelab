package phases

import (
	"fmt"
	"os"

	"homelab/steward/internal/ansible"
	"homelab/steward/internal/config"
	"homelab/steward/internal/run"
)

// Hypervisor configures Proxmox: repos, Tailscale, RBAC, SDN. Safe to re-run.
func Hypervisor(ctx *run.Context) error {
	run.WritePhase("Hypervisor", "Configure Proxmox: repos, Tailscale, RBAC, SDN. Safe to re-run.")

	if _, err := os.Stat(ctx.SiteVars); err != nil {
		return fmt.Errorf("no site.auto.yml - run the Render phase first so the playbook knows this site's network")
	}

	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		return err
	}
	net, err := config.ResolveSiteNetwork(cfg, ctx.Site)
	if err != nil {
		return err
	}

	hosts := make([]string, len(net.Hypervisors))
	for i, h := range net.Hypervisors {
		hosts[i] = h.IP
	}
	if len(hosts) == 0 {
		return fmt.Errorf("no hypervisor addresses resolved for site '%s'. The preflight cannot run, so refusing to continue", ctx.Site)
	}

	_, overlayVarsErr := os.Stat(ctx.OverlayVars)
	haveOverlayVars := overlayVarsErr == nil
	switch {
	case ctx.SkipOverlay:
		run.Info("overlay network skipped - the hypervisor will not join the tailnet")
	case !haveOverlayVars:
		run.Warn("No overlay-network.auto.yml - run the Overlay phase first, or log the host in by hand.")
	}

	if err := ansible.PreflightSSH(hosts); err != nil {
		return err
	}

	extraVars := []string{"-e", "@site.auto.yml"}
	if !ctx.SkipOverlay && haveOverlayVars {
		extraVars = append(extraVars, "-e", "@overlay-network.auto.yml")
	}
	if ctx.SkipOverlay {
		extraVars = append(extraVars, "-e", "configure_overlay=false")
	}
	if ctx.SkipUpgrade {
		extraVars = append(extraVars, "-e", "do_dist_upgrade=false")
	}

	run.Info("running the playbook")
	if err := ansible.RunPlaybook(ctx.HypervisorDir, extraVars); err != nil {
		return err
	}

	run.Ok("hypervisor configured")
	return nil
}
