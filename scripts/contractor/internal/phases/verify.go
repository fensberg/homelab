package phases

import (
	"fmt"
	"time"

	"homelab/contractor/internal/config"
	"homelab/contractor/internal/run"
)

// Verify proves the network works before spending time on OpenTofu.
func Verify(ctx *run.Context) error {
	run.WritePhase("Verify", "Prove the network works before spending time on OpenTofu.")

	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		return err
	}
	net, err := config.ResolveSiteNetwork(cfg, ctx.Site)
	if err != nil {
		return err
	}
	gateway := net.Gateway
	pveHost := net.Hypervisors[0].IP

	// Addresses are not printed. This phase runs during a converge, which runs
	// in a public repository's Actions log, and the hypervisor's address comes
	// from the vault like every other value here - the same reason `plan` and
	// the apply summary report structure and never values. Anyone debugging
	// this locally has config/management.rendered.json in front of them, which
	// is where the address already is.
	run.Info(fmt.Sprintf("checking the Proxmox API for site %s ...", ctx.Site))
	if !run.TestPort(pveHost, 8006, 10*time.Second) {
		return fmt.Errorf("cannot reach site %s's hypervisor on the Proxmox API port. Fix that before continuing (the address is in config/management.rendered.json)", ctx.Site)
	}
	run.Ok("Proxmox API reachable")

	run.Info(fmt.Sprintf("checking the SDN gateway for site %s ...", ctx.Site))
	if !run.Ping(gateway) {
		run.Warn("No reply from the SDN gateway.")
		fmt.Printf(`
  This is the single most common reason ignition hangs. Two usual causes:

    a) The Proxmox SDN was never applied to the kernel.
       On the Proxmox host, run:  ip -br addr show vnetint
       You want to see it UP with the site's node subnet.

    b) The subnet route is advertised but not approved.
       Check the Subnets panel in the Tailscale admin console. If the route
       shows under "Awaiting Approval", the tailnet policy's autoApprovers
       does not cover it - a policy scoped to a narrower CIDR approves that
       one and leaves this range pending. Widen it:

         "autoApprovers": { "routes": { "10.0.0.0/8": ["tag:homelab-router"] } }

       See docs/tailnet-setup.md.

`)
		return fmt.Errorf("site %s's SDN gateway is unreachable - stopping before OpenTofu (the address is in config/management.rendered.json)", ctx.Site)
	}
	run.Ok("SDN gateway reachable - the path to your future nodes works")
	return nil
}
