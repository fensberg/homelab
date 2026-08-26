package phases

import (
	"fmt"
	"time"

	"homelab/ignite/internal/config"
	"homelab/ignite/internal/run"
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

	run.Info(fmt.Sprintf("checking the Proxmox API on %s ...", pveHost))
	if !run.TestPort(pveHost, 8006, 10*time.Second) {
		return fmt.Errorf("cannot reach the Proxmox API at %s:8006. Fix that before continuing", pveHost)
	}
	run.Ok("Proxmox API reachable")

	run.Info(fmt.Sprintf("checking the SDN gateway at %s ...", gateway))
	if !run.Ping(gateway) {
		run.Warn("No reply from " + gateway + ".")
		fmt.Printf(`
  This is the single most common reason ignition hangs. Two usual causes:

    a) The Proxmox SDN was never applied to the kernel.
       On the Proxmox host, run:  ip -br addr show vnetint
       You want to see it UP with %s/24.

    b) The subnet route is advertised but not approved.
       Check the Subnets panel in the Tailscale admin console. If the route
       shows under "Awaiting Approval", the tailnet policy's autoApprovers
       does not cover it - a policy scoped to a narrower CIDR approves that
       one and leaves this range pending. Widen it:

         "autoApprovers": { "routes": { "10.0.0.0/8": ["tag:homelab-router"] } }

       See docs/tailnet-setup.md.

`, gateway)
		return fmt.Errorf("SDN gateway %s is unreachable - stopping before OpenTofu", gateway)
	}
	run.Ok("SDN gateway reachable - the path to your future nodes works")
	return nil
}
