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
	_, compute := tofuDeclaring(t, declMachines)
	_, pools := tofuDeclaring(t, declPools)

	machines := forEachCollections(t, compute, "proxmox_virtual_environment_vm")
	if len(machines) == 0 {
		t.Fatal("found no proxmox_virtual_environment_vm resources with a for_each in modules/infrastructure/cluster/compute.tf, so this test asserts nothing")
	}

	placed := map[string]bool{}
	for _, collection := range forEachCollections(t, pools, "proxmox_pool_membership") {
		placed[collection] = true
	}
	if len(placed) == 0 {
		t.Fatal(`modules/infrastructure/cluster/pools.tf declares no proxmox_pool_membership with a for_each, so no machine is placed.

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

Add a proxmox_pool_membership in modules/infrastructure/cluster/pools.tf iterating the same
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
	_, pools := tofuDeclaring(t, declPools)

	ids := regexp.MustCompile(`(?m)^\s*pool_id\s*=\s*"([^"]*)"`).FindAllStringSubmatch(pools, -1)
	if len(ids) == 0 {
		t.Fatal("no literal pool_id assignment found in modules/infrastructure/cluster/pools.tf, so its scoping has not been checked")
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
