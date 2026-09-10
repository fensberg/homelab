package repo

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every machine this estate builds lands in a pool named for what it is for.
//
// WHAT THIS GUARDS, which is not tidiness. The pools exist so that somebody
// looking at the hypervisor can tell a control plane from a worker without
// coming back here to read HCL. That property does not break by someone
// deleting a pool - that is a visible, deliberate change. It breaks by
// ADDITION: a new class of machine arrives, its resource block is written
// beside the others, and nothing anywhere says it belongs in a pool. The tree
// grows an ungrouped machine and the grouping quietly stops meaning "all of
// them".
//
// So this is fail-closed in the direction the failure actually comes from.
//
// It compares the collection each resource iterates rather than resource names,
// because the collection is what decides which machines exist. A resource
// renamed still has to iterate something, and that something still has to be
// placed in a pool.

// forEachCollections returns the collection each resource of the given type
// iterates, keyed by resource name.
//
// `toset(...)` is unwrapped because it is a type conversion rather than a
// different collection: `toset(local.all_vm_hypervisors)` and
// `local.all_vm_hypervisors` name the same machines, and treating them as
// different would fail on a change that altered nothing.
func forEachCollections(t *testing.T, body, resourceType string) map[string]string {
	t.Helper()
	block := regexp.MustCompile(`(?s)resource\s+"` + regexp.QuoteMeta(resourceType) + `"\s+"([A-Za-z0-9_]+)"\s*\{(.*?)\n\}`)
	forEach := regexp.MustCompile(`(?m)^\s*for_each\s*=\s*(.+?)\s*$`)
	unwrap := regexp.MustCompile(`^toset\((.*)\)$`)

	out := map[string]string{}
	for _, m := range block.FindAllStringSubmatch(body, -1) {
		name, inner := m[1], m[2]
		fe := forEach.FindStringSubmatch(inner)
		if fe == nil {
			continue // A singleton. Handled by the caller if it matters.
		}
		expr := strings.TrimSpace(fe[1])
		if u := unwrap.FindStringSubmatch(expr); u != nil {
			expr = strings.TrimSpace(u[1])
		}
		out[name] = expr
	}
	return out
}

func TestEveryMachineClassIsAssignedAPool(t *testing.T) {
	compute := readRepoFile(t, "management/cluster/compute.tf")
	pools := readRepoFile(t, "management/cluster/pools.tf")

	machines := forEachCollections(t, compute, "proxmox_virtual_environment_vm")
	if len(machines) == 0 {
		t.Fatal("found no proxmox_virtual_environment_vm resources with a for_each in management/cluster/compute.tf, so this test asserts nothing")
	}

	placed := map[string]bool{}
	for _, collection := range forEachCollections(t, pools, "proxmox_pool_membership") {
		placed[collection] = true
	}
	if len(placed) == 0 {
		t.Fatal(`management/cluster/pools.tf declares no proxmox_pool_membership with a for_each, so no machine is placed.

If pools were removed deliberately, remove this test in the same change - a
guard left standing over a reversed decision is noise.`)
	}

	var unplaced []string
	for name, collection := range machines {
		if !placed[collection] {
			unplaced = append(unplaced, name+"  (iterates "+collection+")")
		}
	}
	sort.Strings(unplaced)

	if len(unplaced) > 0 {
		t.Errorf(`%d machine class(es) are built but never placed in a pool:

  %s

Add a proxmox_pool_membership in management/cluster/pools.tf iterating the same
collection, in the pool for the function it serves. The hypervisor groups
machines by what they are for so an operator can tell a control plane from a
worker at a glance, and a class that is missing does not break that visibly -
it just quietly stops being true of everything.`,
			len(unplaced), strings.Join(unplaced, "\n  "))
	}
}

// A pool id has to carry the site, because pools are datacenter-scoped.
//
// Two sites sharing a Proxmox cluster would collide on a bare `control-plane`,
// and the collision does not error - Proxmox adopts the other estate's
// machines into this estate's pool, silently. The config supports several
// sites by design, so the collision is expressible rather than theoretical.
func TestPoolIdsAreScopedToTheSite(t *testing.T) {
	pools := readRepoFile(t, "management/cluster/pools.tf")

	ids := regexp.MustCompile(`(?m)^\s*pool_id\s*=\s*"([^"]*)"`).FindAllStringSubmatch(pools, -1)
	if len(ids) == 0 {
		t.Fatal("no literal pool_id assignment found in management/cluster/pools.tf, so its scoping has not been checked")
	}
	for _, id := range ids {
		if !strings.Contains(id[1], "local.site_name") {
			t.Errorf(`a pool id is %q, which does not include the site.

Pools are datacenter-scoped, so an unprefixed id collides the moment two sites
share a Proxmox cluster - and Proxmox answers a collision by adopting the other
estate's machines into this pool rather than by failing.`, id[1])
		}
	}
}

// The function belongs in the resource NAME, not in a for_each key.
//
// This is a legibility guard with a security reason underneath it. redactKeys
// strips any for_each key that is not purely numeric, because a key can be a
// hypervisor's real hostname - a vault value - and nothing downstream can tell
// a safe key from an unsafe one. So a membership keyed by function rendered as
// nine identical `<redacted>` rows in the plan comment, on a change whose
// entire purpose is that somebody can see what is what.
//
// A resource name cannot carry a vault value: it is an HCL identifier written
// in this file. Keeping the function there is what makes the plan readable
// without weakening redaction, and this fails if somebody consolidates the
// blocks back into one resource over a map of functions.
func TestPoolMembershipNamesTheFunctionInTheResourceName(t *testing.T) {
	pools := readRepoFile(t, "management/cluster/pools.tf")

	memberships := forEachCollections(t, pools, "proxmox_pool_membership")
	if len(memberships) < 2 {
		t.Errorf(`there are %d proxmox_pool_membership resources, so at most one function is named in a resource name.

Consolidating these into one resource over a map of functions puts the function
into a for_each key, and redactKeys cannot tell that key from a hypervisor
hostname - so every row in the plan comment becomes "<redacted>" and the change
that exists to make the estate legible produces a plan nobody can read.`, len(memberships))
	}
	for name := range memberships {
		if strings.Contains(name, "by_function") {
			t.Errorf(`proxmox_pool_membership.%s is named for the grouping rather than for a function.

The plan renders the resource name and redacts the key, so the name is the only
part a reviewer can read. It has to say which pool this is.`, name)
		}
	}
}

// A pool membership must depend on the machine it places, and the only way to
// say that in HCL is to read the vm_id off the VM resource.
//
// WHAT THIS GUARDS. On destroy, OpenTofu reverses the dependency graph - so an
// edge from the membership to the VM is what makes the membership come out
// first, while the VM still exists to be removed from a pool. Without that
// edge the two are unordered, they are deleted concurrently, and deleting the
// VM takes it out of its pool as a side effect. The membership delete that
// loses the race then asks Proxmox to remove a VM that is no longer a member,
// which answers HTTP 500 and fails the teardown.
//
// That is not hypothetical. It happened twice in one session, on
// `templates["<hypervisor>"]`, `control_plane["102"]`, `workers["200"]` and
// `workers["202"]`, and each time it left the estate half-destroyed with state
// deliberately preserved - the "unexploded ordnance" case the destroy path
// exists to avoid.
//
// `each.value.vm_id` reads the same number from a local and creates no edge at
// all, which is why it cannot be spelled that way. The number being identical
// is exactly what makes this easy to get wrong and impossible to see in review.
func TestPoolMembershipDependsOnTheMachineItPlaces(t *testing.T) {
	pools := readRepoFile(t, "management/cluster/pools.tf")

	block := regexp.MustCompile(`(?s)resource\s+"proxmox_pool_membership"\s+"([A-Za-z0-9_]+)"\s*\{(.*?)\n\}`)
	vmID := regexp.MustCompile(`(?m)^\s*vm_id\s*=\s*(.+?)\s*$`)
	referencesVM := regexp.MustCompile(`proxmox_virtual_environment_vm\.[A-Za-z0-9_]+\[[^\]]+\]\.vm_id`)

	found := 0
	for _, m := range block.FindAllStringSubmatch(pools, -1) {
		name, inner := m[1], m[2]
		got := vmID.FindStringSubmatch(inner)
		if got == nil {
			t.Errorf("proxmox_pool_membership %q sets no vm_id", name)
			continue
		}
		found++
		if !referencesVM.MatchString(got[1]) {
			t.Errorf(`proxmox_pool_membership %q takes vm_id from %s.

It must read vm_id off the VM resource itself, so that OpenTofu orders the
membership's destroy before the machine's. Reading the same number out of a
local creates no dependency edge, the two deletes race, and the one that loses
asks Proxmox to remove a VM that is already gone from the pool - HTTP 500, and
a teardown that stops half-done.`, name, got[1])
		}
	}

	if found == 0 {
		t.Fatal("no proxmox_pool_membership resources found - this guard is looking at the wrong file")
	}
}
