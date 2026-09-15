package repo

import (
	"net"
	"regexp"
	"strings"
	"testing"
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
	talos := readRepoFile(t, "management/cluster/talos.tf")

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

func TestThePodAndServiceNetworksCannotCollideWithASite(t *testing.T) {
	variables := readRepoFile(t, "management/cluster/variables.tf")

	// Every site's /16, at the extremes of the octet registry.tf permits.
	var sites []*net.IPNet
	for octet := lowestSiteOctet; octet <= highestSiteOctet; octet++ {
		_, n, err := net.ParseCIDR("10." + itoaSmall(octet) + ".0.0/16")
		if err != nil {
			t.Fatalf("building site /16 for octet %d: %v", octet, err)
		}
		sites = append(sites, n)
	}

	checked := 0
	for _, name := range []string{"pod_cidr", "service_cidr"} {
		m := regexp.MustCompile(name + `\s*=\s*"([^"]+)"`).FindStringSubmatch(variables)
		if m == nil {
			t.Errorf("variables.tf declares no %s, so talos.tf has nothing to read", name)
			continue
		}
		_, cluster, err := net.ParseCIDR(m[1])
		if err != nil {
			t.Errorf("%s is %q, which is not a CIDR", name, m[1])
			continue
		}
		checked++

		for _, site := range sites {
			if overlaps(cluster, site) {
				t.Errorf(`%s is %s, which overlaps the site network %s.

Site octets are asserted 1-95, so that is a range a real site can occupy. Pods
or services sharing addresses with the machines they run on is not a subtle
failure - it is routing that works until the moment two things want the same
address, and clusterNetwork cannot be changed without rebuilding the cluster.`,
					name, m[1], site)
				break
			}
		}
	}
	if checked != 2 {
		t.Fatalf("only %d of the two cluster networks were checked, so this proves "+
			"less than it claims", checked)
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
	for _, path := range []string{
		"management/cluster/variables.tf",
		"management/cluster/compute.tf",
		"management/cluster/talos.tf",
	} {
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
	if checked != 3 {
		t.Fatalf("only %d file(s) were read, so this proves nothing", checked)
	}
	if declared != 1 {
		t.Errorf("local.dns_resolvers is declared %d time(s); it has to be exactly "+
			"one, or it is not a single source", declared)
	}
}
