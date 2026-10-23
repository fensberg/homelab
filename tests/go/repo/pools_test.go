package repo

import (
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
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

// forEachCollections is each resource of a type that is made once per member
// of a collection, by its name, and the collection it iterates: the
// reference its for_each names, with toset looked through. A resource made
// once is left out; the caller handles it if it matters.
func forEachCollections(t *testing.T, resourceType string) map[string]string {
	t.Helper()
	tree := repository(t)
	out := map[string]string{}
	for _, b := range tree.Blocks(t, "resource", resourceType) {
		forEach, ok := b.Body.Attributes["for_each"]
		if !ok {
			continue
		}
		expr := forEach.Expr
		for {
			call, ok := expr.(*hclsyntax.FunctionCallExpr)
			if !ok || call.Name != "toset" || len(call.Args) != 1 {
				break
			}
			expr = call.Args[0]
		}
		if ref, ok := expr.(*hclsyntax.ScopeTraversalExpr); ok {
			out[b.Labels[1]] = traversalName(ref.Traversal)
		} else {
			out[b.Labels[1]] = tree.Source(t, expr)
		}
	}
	return out
}

func TestEveryMachineClassIsAssignedAPool(t *testing.T) {
	machines := forEachCollections(t, "proxmox_virtual_environment_vm")
	if len(machines) == 0 {
		t.Fatal("found no proxmox_virtual_environment_vm resource with a for_each, so this test asserts nothing")
	}

	placed := map[string]bool{}
	for _, collection := range forEachCollections(t, "proxmox_pool_membership") {
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
	tree := repository(t)
	var ids []hclsyntax.Expression
	_ = hclsyntax.VisitAll(tofuFile(t, declaring(t, declPools).File).Body, func(n hclsyntax.Node) hcl.Diagnostics {
		if a, ok := n.(*hclsyntax.Attribute); ok && a.Name == "pool_id" {
			if _, written := a.Expr.(*hclsyntax.TemplateExpr); written {
				ids = append(ids, a.Expr)
			}
		}
		return nil
	})
	if len(ids) == 0 {
		t.Fatal("no pool_id written as a string was found where the pools are declared, so its scoping has not been checked")
	}
	for _, id := range ids {
		if !refersTo(id, "local.site_name") {
			t.Errorf(`a pool id is %q, which does not include the site.

Pools are datacenter-scoped, so an unprefixed id collides the moment two sites
share a Proxmox cluster - and Proxmox answers a collision by adopting the other
estate's machines into this pool rather than by failing.`, tree.Source(t, id))
		}
	}
}
