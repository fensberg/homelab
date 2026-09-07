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
// So this is fail-closed in the direction the failure actually comes from. It
// enumerates the VM resources from the file that declares them and requires
// each one's collection to be assigned a pool. A machine class that is not is
// a red build, not a gap nobody can see.
//
// It compares the for_each collection rather than the resource name, because
// the collection is what decides which machines exist. A resource renamed
// still has to iterate something, and that something still has to be placed.

// vmCollections returns the local each VM resource iterates, keyed by the
// resource name, read from the file that declares them.
//
// `toset(...)` is unwrapped because it is a type conversion rather than a
// different collection - `toset(local.all_vm_hypervisors)` and
// `local.all_vm_hypervisors` name the same set of machines, and a test that
// treated them as different would fail on a change that altered nothing.
func vmCollections(t *testing.T, body string) map[string]string {
	t.Helper()
	block := regexp.MustCompile(`(?s)resource\s+"proxmox_virtual_environment_vm"\s+"([A-Za-z0-9_]+)"\s*\{(.*?)\n\}`)
	forEach := regexp.MustCompile(`(?m)^\s*for_each\s*=\s*(.+?)\s*$`)
	unwrap := regexp.MustCompile(`^toset\((.*)\)$`)

	out := map[string]string{}
	for _, m := range block.FindAllStringSubmatch(body, -1) {
		name, inner := m[1], m[2]
		fe := forEach.FindStringSubmatch(inner)
		if fe == nil {
			// A VM declared without for_each is a single machine. It still has
			// to be placed, and there is nowhere for this test to look, so say
			// so rather than skipping it.
			t.Errorf(`proxmox_virtual_environment_vm.%s declares no for_each.

This test places machines by the collection they iterate. A singleton VM has
none, so extend this to name it directly rather than leaving it unplaced.`, name)
			continue
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

	collections := vmCollections(t, compute)
	if len(collections) == 0 {
		t.Fatal("found no proxmox_virtual_environment_vm resources in management/cluster/compute.tf, so this test asserts nothing")
	}

	// The membership local is the one place a machine is given a function.
	// Reading only that block, rather than the whole file, keeps a mention in
	// a comment from counting as a placement.
	_, after, ok := strings.Cut(pools, "pool_members = merge(")
	if !ok {
		t.Fatal(`management/cluster/pools.tf declares no pool_members, so nothing places any machine.

If pools have been removed deliberately, remove this test in the same change -
a guard left standing over a decision that was reversed is noise.`)
	}
	members, _, ok := strings.Cut(after, "\n  )")
	if !ok {
		t.Fatal("pool_members is not terminated where this expects; the block could not be read, so it has not been checked")
	}

	var unplaced []string
	for name, collection := range collections {
		if !strings.Contains(members, collection) {
			unplaced = append(unplaced, name+"  (iterates "+collection+")")
		}
	}
	sort.Strings(unplaced)

	if len(unplaced) > 0 {
		t.Errorf(`%d machine class(es) are built but never placed in a pool:

  %s

Add each to pool_members in management/cluster/pools.tf, choosing the function
it serves. The hypervisor groups machines by what they are for so an operator
can tell a control plane from a worker at a glance, and a class that is missing
does not break that visibly - it just quietly stops being true of everything.`,
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

	id := regexp.MustCompile(`(?m)^\s*pool_id\s*=\s*"([^"]*)"`).FindStringSubmatch(pools)
	if id == nil {
		t.Fatal("no literal pool_id assignment found in management/cluster/pools.tf, so its scoping has not been checked")
	}
	if !strings.Contains(id[1], "local.site_name") {
		t.Errorf(`pool ids are %q, which does not include the site.

Pools are datacenter-scoped, so an unprefixed id collides the moment two sites
share a Proxmox cluster - and Proxmox answers a collision by adopting the other
estate's machines into this pool rather than by failing.`, id[1])
	}
}
