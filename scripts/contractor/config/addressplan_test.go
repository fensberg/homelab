package config

import (
	"testing"

	"homelab/details/repopath"
)

// Every site gets its own pods and services, site0 included, answered for the
// whole estate in one question.
func TestEstateRangesAreEachSitesOwn(t *testing.T) {
	root, err := repopath.Root()
	if err != nil {
		t.Fatal(err)
	}
	got, err := EstateRanges(root, map[string]Site{
		"site0": {Octet: 10, ControlPlaneCount: 1},
		"site1": {Octet: 20, ControlPlaneCount: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Ranges{
		"site0": {Site: "10.10.0.0/16", Pods: "10.110.0.0/16", Services: "10.196.40.0/22"},
		"site1": {Site: "10.20.0.0/16", Pods: "10.120.0.0/16", Services: "10.196.80.0/22"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d sites, want %d: %v", len(got), len(want), got)
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s: got %+v, want %+v", k, got[k], w)
		}
	}
}

// The nodes' vnet is the plan's, and reaches the resolved network the Render
// phase writes for the playbook.
func TestTheNodesVNetIsThePlans(t *testing.T) {
	cfg := &Config{Tunnel: validTunnel(), Sites: map[string]Site{"site0": validSite()}}
	net, err := ResolveSiteNetwork(cfg, "site0")
	if err != nil {
		t.Fatal(err)
	}
	if net.VNet == "" {
		t.Fatal("the resolved network names no vnet, so the playbook would be handed none")
	}
	for _, z := range net.DMZZones {
		if z.VNet == net.VNet {
			t.Errorf("zone %s shares the nodes' vnet %s", z.Name, z.VNet)
		}
	}
}
