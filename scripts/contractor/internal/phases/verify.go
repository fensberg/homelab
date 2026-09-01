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

	// How the path to the node subnet is proven depends on who is asking, and
	// the two callers are in genuinely different situations.
	//
	// Ignition runs from a workstation, before any node exists. There is
	// nothing on that subnet to talk to yet, so the gateway answering an ICMP
	// echo is the only proof available - and it is a good one, because it says
	// the SDN was applied to the kernel and the route was approved.
	//
	// A converge runs from a pod inside the cluster, on an estate whose nodes
	// already exist. It cannot send ICMP at all: Pod Security Admission
	// requires capabilities to be dropped, NET_RAW with them, so `ping` fails
	// regardless of whether anything is reachable. Measured from a pod on this
	// estate - ICMP to the gateway fails while TCP to a node on the same
	// subnet succeeds, at the same moment.
	//
	// Granting the runner NET_RAW to satisfy a pre-flight would be exchanging
	// a real security boundary for a check, which is backwards. So the
	// converge proves the same property a different way: a node answering on
	// the subnet means the path to the subnet works, which is what the gateway
	// ping was standing in for.
	if ctx.PreexistingEstate {
		return verifyNodeSubnetReachable(ctx, net.NodeIPs, 5*time.Second)
	}

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

// verifyNodeSubnetReachable proves the path to the node subnet without ICMP,
// for callers that cannot send it.
//
// Any declared node answering on the Kubernetes API port is sufficient: the
// question is whether this caller can reach that subnet at all, not whether
// every node is well. Node health is the Health phase's job and it asks a much
// harder question than this one.
// The timeout is a parameter so the tests do not have to spend it. A probe
// that cannot be exercised quickly is a probe nobody exercises.
func verifyNodeSubnetReachable(ctx *run.Context, nodeIPs []string, timeout time.Duration) error {
	if len(nodeIPs) == 0 {
		return fmt.Errorf("site %s declares no node addresses, so there is nothing to prove the path against", ctx.Site)
	}

	run.Info(fmt.Sprintf("checking the node subnet for site %s ...", ctx.Site))
	for _, ip := range nodeIPs {
		if run.TestPort(ip, 6443, timeout) {
			run.Ok("node subnet reachable - the path to this estate works")
			return nil
		}
	}

	// Addresses are deliberately absent, for the reason given above: this runs
	// in a public repository's Actions log.
	return fmt.Errorf(`no node in site %s answered on the Kubernetes API port.

None of the %d declared node addresses accepted a connection, so this caller
cannot reach the node subnet at all. The addresses are in
config/management.rendered.json.

Two usual causes:

  a) The estate is genuinely down. Check the nodes before anything else.
  b) This caller has no route to that subnet. A converge runs inside the
     cluster and reaches it directly; anything running elsewhere needs the
     overlay, and the overlay needs the hypervisor to be carrying traffic
     rather than merely registered - see docs/epochs/01-ignition.md`, ctx.Site, len(nodeIPs))
}
