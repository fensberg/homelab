package repo

import (
	"gopkg.in/yaml.v3"
	"homelab/tests/parsed"
	"regexp"
	"slices"
	"strings"
	"testing"
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
	docs := repository(t).YAMLDocuments(t, "workstation/provision.yml")

	onHypervisorBridge := slices.ContainsFunc(scalarsAt(docs, "bridge"), regexp.MustCompile(`^vmbr\d+$`).MatchString)

	// Only the command matters. The explanation above it says the words on
	// purpose, and the parse leaves that comment out - which is the exact
	// shape of test this repository has been bitten by twice.
	var accepts bool
	for _, v := range commandsIn(t, docs) {
		if strings.Contains(v, "tailscale up") && strings.Contains(v, "--accept-routes") {
			accepts = true
		}
	}

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

// commandsIn is every string the playbook holds, and every string of the
// cloud-init documents it carries as templates, each read as YAML once its
// Jinja control lines are set aside - so a comment in the template is no
// more a command than one in the playbook.
func commandsIn(t *testing.T, docs []any) []string {
	t.Helper()
	var out []string
	for _, v := range parsed.Values(docs) {
		var kept []string
		for _, line := range strings.Split(v, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "{%") {
				kept = append(kept, line)
			}
		}
		var embedded any
		if strings.Contains(v, "\n") && yaml.Unmarshal([]byte(strings.Join(kept, "\n")), &embedded) == nil {
			if _, isText := embedded.(string); !isText {
				out = append(out, parsed.Values(embedded)...)
				continue
			}
		}
		out = append(out, v) // one line, or prose or a script: not a document

	}
	return out
}
