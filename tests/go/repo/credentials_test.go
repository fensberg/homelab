package repo

import (
	"regexp"
	"strings"
	"testing"
)

// A rendered credential carries the estate, so the operator does not have to.
//
// WHY THIS IS NOT THE ONLY GUARD, and why it is still worth having. The real
// refusal lives in scripts/contractor: writeTalosconfigTo re-reads what it
// rendered and refuses to hand over a talosconfig that names no nodes. That is
// the load-bearing one, because it fires on what OpenTofu actually produced
// rather than on what the HCL appears to ask for - a provider that stopped
// honouring the argument would slip past anything static.
//
// It fires at the moment of use, though, which is a real diagnostic in front
// of a real operator. This one fires on the branch that removed the argument,
// which is cheaper by a whole incident.
//
// WHAT IT ASSERTS, deliberately narrowly: that `nodes` is set to something.
// Not what it is set to. `local.node_ips` today, worker addresses or a subset
// tomorrow - all of those are decisions this test has no business holding an
// opinion on. A test that pinned the expression would be a change detector,
// passing forever while the behaviour rotted and failing on every rename.
func TestTheRenderedTalosconfigNamesNodesAndEveryEndpoint(t *testing.T) {
	body := readRepoFile(t, "management/cluster/talos.tf")

	block := regexp.MustCompile(`(?s)data\s+"talos_client_configuration"\s+"this"\s*\{(.*?)\n\}`).FindStringSubmatch(body)
	if block == nil {
		t.Fatal(`management/cluster/talos.tf declares no data "talos_client_configuration" "this".

That data source is the whole talosconfig. If it has been renamed, this test is
asserting nothing and needs to follow it.`)
	}

	assigned := func(arg string) string {
		m := regexp.MustCompile(`(?m)^\s*` + arg + `\s*=\s*(.+)$`).FindStringSubmatch(block[1])
		if m == nil {
			return ""
		}
		return strings.TrimSpace(m[1])
	}

	nodes := assigned("nodes")
	switch nodes {
	case "":
		t.Error(`the rendered talosconfig sets no nodes.

The Talos provider leaves the field empty unless it is told otherwise, and an
empty one is not a broken credential - it is a working credential on which every
node-targeted command refuses on first use and has to be re-run with -n. Node
addresses are exactly what this repository keeps in the vault so the config
shows the estate's shape without revealing it, so making the operator supply one
puts back the piece the design took out (#235).`)
	case "[]":
		t.Error("the rendered talosconfig sets nodes to an empty list, which is the same as not setting it")
	}

	// An endpoint is what talosctl connects *to*. One means the credential is
	// useless precisely when the machine it names is the one being diagnosed,
	// which is the case a diagnostic exists for.
	if ep := assigned("endpoints"); strings.Contains(ep, "[0]") {
		t.Errorf(`the talosconfig's endpoints are %s - a single machine.

Every control plane proxies the Talos API, so naming one of them buys nothing
and loses the credential the moment that machine is the problem.`, ep)
	}
}
