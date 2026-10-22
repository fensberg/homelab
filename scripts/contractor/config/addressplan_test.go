package config

import (
	"strings"
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
	cfg := &Config{Tunnel: validTunnel(), Certificates: validCertificates(), Sites: map[string]Site{"site0": validSite()}}
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

// Every machine the site is to have is known with its hypervisor and its
// size, and every id that is the site's is known, its template's among
// them. A hypervisor's memory is budgeted from these: a machine with no
// hypervisor would be held against no host, and a site's own template
// counted as somebody else's would be taken off the site twice.
func TestEveryPlannedMachineHasAHypervisorAndASizeAndTheSitesIdsAreKnown(t *testing.T) {
	site := validSite()
	cfg := &Config{Tunnel: validTunnel(), Certificates: validCertificates(), Sites: map[string]Site{"site0": site}}
	net, err := ResolveSiteNetwork(cfg, "site0")
	if err != nil {
		t.Fatal(err)
	}
	if want := site.ControlPlaneCount + site.WorkerCount; len(net.Machines) < want {
		t.Fatalf("%d machines are planned, and the site asks for at least %d", len(net.Machines), want)
	}
	reserved := map[int]bool{}
	for _, id := range net.Reserved {
		reserved[id] = true
	}
	if !reserved[net.TemplateVMID] {
		t.Error("the site's own template is not among its ids")
	}
	planes := 0
	for _, m := range net.Machines {
		if m.Hypervisor == "" {
			t.Errorf("machine %d is planned onto no hypervisor", m.VMID)
		}
		if m.MemoryBytes != MemoryOf[m.Role] || m.MemoryBytes == 0 {
			t.Errorf("machine %d, a %s, is given %d bytes", m.VMID, m.Role, m.MemoryBytes)
		}
		if !reserved[m.VMID] {
			t.Errorf("machine %d is the site's and is not among its ids", m.VMID)
		}
		if m.Role == ControlPlane {
			planes++
		}
	}
	if planes != site.ControlPlaneCount {
		t.Errorf("%d control planes are planned, and the site asks for %d", planes, site.ControlPlaneCount)
	}
}

// Each zone machine is known by the key the address plan gives it, under its
// name: the cluster module's resources for that machine are under that key,
// and a converge that has to send one machine its configuration again names
// it by that key.
func TestEveryZoneMachineIsKnownByItsKeyInTheAddressPlan(t *testing.T) {
	site := validSite()
	site.DMZZones = map[string]DMZZone{"zone0": {}}
	cfg := &Config{Tunnel: validTunnel(), Certificates: validCertificates(), Sites: map[string]Site{"site0": site}}
	net, err := ResolveSiteNetwork(cfg, "site0")
	if err != nil {
		t.Fatal(err)
	}
	if len(net.DMZNames) == 0 || len(net.DMZKeys) != len(net.DMZNames) {
		t.Fatalf("%d zone machines are named and %d have a key", len(net.DMZNames), len(net.DMZKeys))
	}
	for _, name := range net.DMZNames {
		host := name[strings.LastIndex(name, "-")+1:]
		if got := net.DMZKeys[name]; got != "zone0-"+host {
			t.Errorf("%s is under %q, and the address plan keys it zone0-%s", name, got, host)
		}
	}
}
