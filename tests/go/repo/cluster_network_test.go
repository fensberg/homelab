package repo

import (
	"net"
	"regexp"
	"sort"
	"strings"
	"testing"

	"homelab/contractor/config"
)

// The pod and service networks are declared, and cannot collide with a site.
//
// WHAT WAS WRONG. `management/cluster/talos.tf` set no clusterNetwork fields,
// so the cluster's two largest address ranges were whatever Talos defaulted to
// (#240). The values in force were correct and nobody had chosen them: they
// appear in none of the addressing decision in docs/epochs/02-abstraction.md,
// which records every other range in the estate down to the VM id bands.
//
// WHY A GUARD RATHER THAN A COMMENT. clusterNetwork is fixed at cluster
// creation, so a wrong value costs a rebuild rather than an edit - and the
// failure mode is silence, which is the one a comment cannot catch. Somebody
// widening the pod range to 10.0.0.0/8 because a CNI's documentation suggests
// it would swallow every site subnet the scheme defines, and nothing else in
// this repository would notice.

var (
	podSubnetsDecl     = regexp.MustCompile(`podSubnets\s*=\s*\[([^\]]*)\]`)
	serviceSubnetsDecl = regexp.MustCompile(`serviceSubnets\s*=\s*\[([^\]]*)\]`)
)

// The site octet is asserted 1-95 by registry.tf, so this is the widest range
// any site's /16 can occupy. A cluster range overlapping it collides with a
// real network rather than a hypothetical one.
const (
	lowestSiteOctet  = 1
	highestSiteOctet = 95
)

func TestThePodAndServiceNetworksAreDeclared(t *testing.T) {
	_, talos := tofuDeclaring(t, declMachineConfig)

	for _, decl := range []struct {
		name string
		re   *regexp.Regexp
	}{
		{"podSubnets", podSubnetsDecl},
		{"serviceSubnets", serviceSubnetsDecl},
	} {
		if !decl.re.MatchString(talos) {
			t.Errorf(`talos.tf declares no %s, so the cluster takes whatever the
platform defaults to and the estate's largest address commitment is a value
nobody chose and nobody can review.

It is fixed at cluster creation, so the cost of getting it wrong is a rebuild.`, decl.name)
		}
	}
}

// Asked of the address plan rather than read from a file, because the ranges
// are computed now: every site gets its own pods and services (epoch 02), and
// whether they collide is a property of the computation over every octet a
// site may have, not of one value somebody wrote down.
func TestThePodAndServiceNetworksCannotCollideWithASite(t *testing.T) {
	// The cluster root takes both from the plan, or the plan's answer below
	// describes nothing the cluster runs.
	variables := tofuAll(t)
	for _, name := range []string{"pod_cidr", "service_cidr"} {
		if !regexp.MustCompile(name + `\s*=\s*local\.net\.` + name + `\b`).MatchString(variables) {
			t.Errorf("nothing takes %s from the address plan (local.net.%s), so the range the "+
				"cluster runs is not the one the plan allocated and checked", name, name)
		}
	}

	// Every site the octet range permits, at once: the plan answers for the
	// estate, and a collision between two sites is as real as one with a site.
	sites := map[string]config.Site{}
	for octet := lowestSiteOctet; octet <= highestSiteOctet; octet++ {
		sites["s"+itoaSmall(octet)] = config.Site{Octet: octet, ControlPlaneCount: 1}
	}
	plan, err := config.EstateRanges(repoRoot(t), sites)
	if err != nil {
		t.Fatalf("asking the address plan: %v", err)
	}
	if len(plan) != len(sites) {
		t.Fatalf("the plan answered for %d of %d sites, so this proves less than it claims", len(plan), len(sites))
	}

	type owned struct {
		site, kind string
		n          *net.IPNet
	}
	var all []owned
	for key, r := range plan {
		for kind, cidr := range map[string]string{"site network": r.Site, "pods": r.Pods, "services": r.Services} {
			_, n, err := net.ParseCIDR(cidr)
			if err != nil {
				t.Fatalf("%s's %s is %q, which is not a CIDR", key, kind, cidr)
			}
			all = append(all, owned{key, kind, n})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].site+all[i].kind < all[j].site+all[j].kind })
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			a, b := all[i], all[j]
			if a.kind == "site network" && b.kind == "site network" {
				continue // distinct octets are registry.tf's to assert
			}
			if overlaps(a.n, b.n) {
				t.Errorf(`%s's %s (%s) overlaps %s's %s (%s).

Site octets are asserted 1-95, so both are ranges real sites can hold. Pods or
services sharing addresses with machines, or with another site's, is routing
that works until two things want the same address, and clusterNetwork cannot
be changed without rebuilding the cluster.`, a.site, a.kind, a.n, b.site, b.kind, b.n)
			}
		}
	}
}

// Cilium takes its pod addresses from Kubernetes, and that is load-bearing.
//
// This is the half worth guarding rather than the one #240 assumed. That issue
// warned that Cilium's default cluster pool is 10.0.0.0/8 and would collide
// with every site subnet - true of `ipam: cluster-pool`, and this estate runs
// `ipam: kubernetes`, which delegates to the podSubnets declared above and has
// no pool of its own.
//
// So the hazard is not live; it is one word away. Switching the mode would move
// address allocation from a range this repository declares to a default it does
// not, and the manifest would still render, still install, and still come up.
func TestCiliumTakesItsPodAddressesFromKubernetes(t *testing.T) {
	manifest := readRepoFile(t, "clusters/bootstrap/cilium.yaml")

	m := regexp.MustCompile(`(?m)^\s*ipam:\s*"?([a-z-]+)"?\s*$`).FindStringSubmatch(manifest)
	if m == nil {
		t.Fatal("the rendered Cilium manifest declares no ipam mode, so this cannot " +
			"tell which range pods are allocated from")
	}
	if m[1] != "kubernetes" {
		t.Errorf(`Cilium's ipam mode is %q, not "kubernetes".

In every other mode Cilium allocates pod addresses from a pool of its own, and
its default pool is 10.0.0.0/8 - which contains every site subnet this estate
can define, because site octets run 1-95. The podSubnets declared in talos.tf
would then describe nothing.

If the mode is being changed deliberately, the pool has to be declared with it
and checked against the site scheme the same way, or this estate has gone back
to an undeclared default.`, m[1])
	}
}

func overlaps(a, b *net.IPNet) bool {
	return a.Contains(b.IP) || b.Contains(a.IP)
}

func itoaSmall(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// The resolvers are declared once and read everywhere.
//
// They were written out four times - twice in compute.tf's cloud-init and twice
// in talos.tf's ResolverConfig (#360). Four copies of one value is the exact
// duplication scripts/versions.env exists to refuse for tool versions, and it
// fails the same way: three of them get updated and the fourth machine class
// resolves through something nobody meant.
//
// It is also the estate's only documented route for a fork on a network with
// internal resolvers or a policy against public upstreams. One line to change
// is a route; four lines to find is a bug waiting.
func TestTheDNSResolversAreDeclaredInOnePlace(t *testing.T) {
	declaration := regexp.MustCompile(`dns_resolvers\s*=\s*\[`)
	// An IPv4 literal, which is what a restatement looks like. Addresses that
	// belong to the estate's own scheme are not resolvers and are skipped.
	literal := regexp.MustCompile(`"(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})"`)

	declared := 0
	checked := 0
	// Every OpenTofu file, not a list of the three that held the copies: a
	// restatement in a file nobody listed is the one this exists to find.
	for _, path := range tracked(t, func(rel string) bool {
		return strings.HasSuffix(rel, ".tf") && !strings.Contains(rel, "/tests/")
	}) {
		body := readRepoFile(t, path)
		checked++
		for _, line := range strings.Split(body, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue // Prose about the decision is not the decision.
			}
			if declaration.MatchString(line) {
				declared++
				continue
			}
			if !strings.Contains(line, "dns") && !strings.Contains(line, "nameserver") &&
				!strings.Contains(line, "servers") {
				continue
			}
			if m := literal.FindStringSubmatch(line); m != nil {
				t.Errorf(`%s names the resolver %s directly:

    %s

The resolvers are declared once in variables.tf as local.dns_resolvers, because
this value was restated four times and a fork changing it had to find all four.
Read it from there.`, path, m[1], trimmed)
			}
		}
	}
	if checked < 10 {
		t.Fatalf("only %d file(s) were read, so the enumeration has stopped matching", checked)
	}
	if declared != 1 {
		t.Errorf("local.dns_resolvers is declared %d time(s); it has to be exactly "+
			"one, or it is not a single source", declared)
	}
}

// A Talos image's datastore path changes when its schematic does.
//
// WHAT WAS WRONG. `proxmox_download_file` is identified by its datastore path,
// and the path was `talos-<version>.iso`. A schematic id says WHICH extensions
// are baked into the image, so re-minting one - the only way to add, remove or
// move an extension - produced no plan diff at all: same name, same resource,
// nothing to do (#97). The new image was never fetched, and the
// `replace_triggered_by` on the template could not fire, because the thing it
// triggers on had not changed.
//
// Confirmed at the time by a real plan against a live estate that showed only a
// tailnet key being replaced.
//
// WHY A GUARD. The failure is silence. A schematic bump looks applied - the
// config says the new id, the plan is clean, the apply succeeds - and every
// node keeps the old extensions. There is nothing red anywhere, which is how it
// survived long enough to be found by reading a plan for something else.
//
// This asserts the identity only. Whether a rebuilt TEMPLATE reaches machines
// that are already running is node lifecycle, which epoch 05 owns and this does
// not claim.
func TestATalosImagePathCarriesItsSchematic(t *testing.T) {
	_, compute := tofuDeclaring(t, declMachines)

	names := regexp.MustCompile(`(?m)^\s*file_name\s*=\s*"([^"]+)"`).FindAllStringSubmatch(compute, -1)
	const bothImages = 2
	if len(names) < bothImages {
		t.Fatalf(`found %d file_name declaration(s) in compute.tf, and there are two -
the estate's image and the untrusted zone's.

The parse has stopped matching, so an image whose path cannot change is no
longer being checked for.`, len(names))
	}

	for _, m := range names {
		name := m[1]
		if !strings.Contains(name, "schematic_id") {
			t.Errorf(`the image path %q does not carry its schematic id.

The resource is identified by that path, so re-minting a schematic is not a
change: the image is never re-fetched, and the replace_triggered_by on the
template has nothing to fire on. A schematic bump then looks applied - clean
plan, successful apply - while every node keeps the old extensions.

That is how a known-vulnerable Tailscale reached every machine in the estate
and stayed there.`, name)
		}
		if !strings.Contains(name, "talos_version") {
			t.Errorf(`the image path %q does not carry the Talos version.

The version resolves every extension's build, so two images from one schematic
on different Talos releases are different bytes at the same path.`, name)
		}
	}
}

// Every machine is told the cluster's network, not only the control planes.
//
// A kubelet tells its pods to ask the tenth address of its own machine's
// serviceSubnets for DNS. The network patch sat on the control planes' config
// alone, so the workers took Talos's default range and pointed every pod at
// 10.96.0.10, while cluster DNS answered in the site's own range: nothing on a
// worker could resolve a name, and site0's rebuild stopped at Health (#578).
// It was invisible for as long as the declared ranges equalled the defaults.
//
// Asserted of every talos_machine_configuration in talos.tf, discovered rather
// than listed, so a machine class added later is held to it too.
func TestEveryMachineIsToldTheClustersNetwork(t *testing.T) {
	// Every machine configuration, in whichever file: a class declared
	// somewhere else is a class this must still hold to it.
	talos := tofuAll(t)
	blocks := regexp.MustCompile(`(?s)data "talos_machine_configuration" "([^"]+)" \{(.*?)\n\}`).FindAllStringSubmatch(talos, -1)
	if len(blocks) < 3 {
		t.Fatalf("found %d machine configurations in talos.tf; there are at least three classes, so the reader has stopped matching", len(blocks))
	}
	for _, b := range blocks {
		if !strings.Contains(b[2], "local.cluster_network_patch") {
			t.Errorf(`the %q machine configuration is not given local.cluster_network_patch.

Its kubelet then takes Talos's default service range, and tells every pod on
that machine to ask for DNS at an address nothing answers on (#578).`, b[1])
		}
	}
}
