package phases

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"homelab/contractor/config"
	"homelab/contractor/internal/budget"
	"homelab/contractor/internal/run"
	"homelab/details/hypervisorapi"
	"homelab/details/onepassword"
)

// hardwareInput is the cluster root's variable that carries what each of a
// site's hypervisors has: read off the host and handed over, and never typed
// into the config. The root outputs it again unchanged, so the state holds
// it, the as-built record carries it, and a pull request's plan - which can
// ask no hypervisor anything - is given the facts as they were at the last
// converge.
const hardwareInput = "hardware"

// handOverHardware asks each of the site's hypervisors what it has and puts
// the answers where every tofu run in the cluster root will find them. Once
// for a run: a converge reaches the root step by step, and the hardware does
// not change between steps.
func handOverHardware(ctx *run.Context) error {
	if os.Getenv(hardwareAsked) == ctx.Site {
		return nil
	}
	hosts, site, err := surveySite(ctx)
	if err != nil {
		return fmt.Errorf(`could not read what this site's hypervisor has, so nothing can be sized against it: %w

Nothing has been changed`, err)
	}
	// And held against what the site asks of it, before tofu is given
	// anything. The sum is printed whether it fits or not: figures of a
	// host's size and no value from the vault, and the only way to see how
	// near the edge a site is running.
	lines, err := budget.Of(hosts, site)
	if err != nil {
		return err
	}
	for _, line := range lines {
		run.Info(line.String())
	}
	if err := budget.Refusal(lines); err != nil {
		return fmt.Errorf("%w.\n\nNothing has been changed", err)
	}
	facts, err := json.Marshal(map[string]any{"nodes": hosts})
	if err != nil {
		return err
	}
	if err := os.Setenv("TF_VAR_"+hardwareInput, string(facts)); err != nil {
		return err
	}
	return os.Setenv(hardwareAsked, ctx.Site)
}

// hardwareAsked marks, in this process's own environment, that the hardware
// has been read for this run.
const hardwareAsked = "CONTRACTOR_HARDWARE_ASKED"

// readHardware is every hypervisor of the site, by its key in the config,
// asked over its own API with the provisioning token and verified against
// its own authority - and the site as planned, which is what the answers
// are held against.
func readHardware(ctx *run.Context) (map[string]hypervisorapi.Host, *config.SiteNetwork, error) {
	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		return nil, nil, err
	}
	site, ok := cfg.Sites[ctx.Site]
	if !ok {
		return nil, nil, fmt.Errorf("the config has no site %q", ctx.Site)
	}
	planned, err := config.ResolveSiteNetwork(cfg, ctx.Site)
	if err != nil {
		return nil, nil, err
	}
	authority, err := onepassword.Read(hypervisorapi.AuthorityRef(ctx.Site))
	if err != nil {
		return nil, nil, fmt.Errorf("the vault does not hold the authority the hypervisor answers under; the hypervisor phase stores it (task configure-hypervisor SITE=%s): %w", ctx.Site, err)
	}
	auth := fmt.Sprintf("PVEAPIToken=%s=%s", site.Hypervisor.TokenID, site.Hypervisor.TokenSecret)
	hosts := map[string]hypervisorapi.Host{}
	for key, node := range site.Hypervisor.Nodes {
		client, err := hypervisorapi.Client(authority, node.Hostname, 15*time.Second)
		if err != nil {
			return nil, nil, err
		}
		host, err := hypervisorapi.Survey(client, fmt.Sprintf("https://%s:8006/api2/json", node.IP), node.Hostname, auth)
		if err != nil {
			return nil, nil, fmt.Errorf("hypervisor %s: %w", key, err)
		}
		hosts[key] = host
	}
	return hosts, planned, nil
}
