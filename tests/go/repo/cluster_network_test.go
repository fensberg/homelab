package repo

import (
	"net"
	"sort"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"homelab/contractor/config"
	"homelab/tests/parsed"
)

// The pod and service networks are declared, and cannot collide with a site.
//
// WHAT WAS WRONG. `modules/infrastructure/cluster/talos.tf` set no clusterNetwork fields,
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
//
// That every machine is told both ranges is a rule over the plan
// (policy/conftest/rules/plan_cluster_network/plan_cluster_network.rego). What is left here is whether
// the ranges the address plan computes can collide, which is a test of that
// module's arithmetic and goes down to its own tests.

// The site octet is asserted 1-95 by registry.tf, so this is the widest range
// any site's /16 can occupy. A cluster range overlapping it collides with a
// real network rather than a hypothetical one.
const (
	lowestSiteOctet  = 1
	highestSiteOctet = 95
)

// Asked of the address plan rather than read from a file, because the ranges
// are computed now: every site gets its own pods and services (epoch 02), and
// whether they collide is a property of the computation over every octet a
// site may have, not of one value somebody wrote down.
func TestThePodAndServiceNetworksCannotCollideWithASite(t *testing.T) {
	light(t)
	// The cluster root takes both from the plan, or the plan's answer below
	// describes nothing the cluster runs.
	for _, name := range []string{"pod_cidr", "service_cidr"} {
		if !takenFromThePlan(t, name) {
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
	plan := repository(t).EstateRanges(t, sites)
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

// takenFromThePlan says whether something in the repository's OpenTofu is
// given this value from the address plan: an argument or a key of that name
// set to local.net.<name>.
func takenFromThePlan(t *testing.T, name string) bool {
	t.Helper()
	isThePlans := func(e hclsyntax.Expression) bool {
		ref, ok := e.(*hclsyntax.ScopeTraversalExpr)
		return ok && traversalName(ref.Traversal) == "local.net."+name
	}
	for _, f := range repository(t).OpenTofu(t) {
		found := false
		_ = hclsyntax.VisitAll(f.Body, func(n hclsyntax.Node) hcl.Diagnostics {
			if a, ok := n.(*hclsyntax.Attribute); ok && a.Name == name && isThePlans(a.Expr) {
				found = true
			}
			return nil
		})
		for _, item := range objectItems(f.Body) {
			found = found || parsed.KeyName(item) == name && isThePlans(item.ValueExpr)
		}
		if found {
			return true
		}
	}
	return false
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
