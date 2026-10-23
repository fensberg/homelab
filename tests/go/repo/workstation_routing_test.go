package repo

import (
	"regexp"
	"slices"
	"testing"

	"homelab/tests/parsed"
)

// A workstation on the hypervisor's own bridge must not accept the estate's
// advertised subnet route.
//
// The two settings are a pair, and the file states one of them plainly:
// `bridge: vmbr0`, chosen so the development machine sits on the LAN rather
// than on the cluster network. Given that, accepting the advertised route for
// the node subnet does not add a path - it replaces the working one.
//
// The node subnet lives in an EVPN VRF, which is a separate routing table with
// no route back to the tailnet. Measured on the hypervisor: from inside the
// VRF it cannot reach a tailnet address, and a socket cannot bind the SDN
// gateway address at all unless it is VRF-bound. Traffic sent over the overlay
// arrives, is handed into the VRF, and nothing returns.
//
// tailscaled's routing table is consulted at rule 5270, ahead of the main
// table at 32766, so the overlay route wins over the LAN route the bridge
// already provides. The Verify phase halted on an unreachable SDN gateway for
// three ignition attempts before this was understood, and the reasoning
// written beside the flag asserted the opposite - that accepting the route was
// what made the subnet reachable.
//
// This is a relationship rather than a spelling: it fails only when both facts
// are true at once, so moving the workstation off the bridge is allowed and
// re-adding the flag while it stays there is not.
func TestAWorkstationOnTheHypervisorBridgeDoesNotAcceptRoutes(t *testing.T) {
	bridge, _ := workstationPlay(t).Vars["bridge"].(string)
	onHypervisorBridge := regexp.MustCompile(`^vmbr\d+$`).MatchString(bridge)
	accepts := slices.ContainsFunc(tailscaleUps(workstationCommands(t)), func(c parsed.Command) bool {
		_, given := flagOf(c, "--accept-routes")
		return given
	})
	if onHypervisorBridge && accepts {
		t.Error(`workstation/provision.yml puts the workstation on the hypervisor's own
bridge AND passes --accept-routes to tailscale up.

Those are incompatible. The node subnet is in an EVPN VRF with no route back
to the tailnet, so the advertised route is a path that carries requests and
returns nothing - and tailscaled's table is consulted before the main one, so
it wins over the LAN route the bridge already provides. Ignition then fails at
Verify on an unreachable SDN gateway.

Either drop --accept-routes, or move the workstation off vmbr0 and accept that
it reaches the estate only as a tailnet peer.`)
	}
	if !onHypervisorBridge && !accepts {
		t.Log("the workstation is no longer on the hypervisor's bridge; whether it " +
			"needs --accept-routes is a live question again rather than a settled one")
	}
}
