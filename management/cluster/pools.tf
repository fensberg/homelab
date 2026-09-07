# =============================================================================
# Resource pools: the hypervisor's machine list, grouped by what a machine is for.
# Vendor: Proxmox VE (bpg/proxmox provider).
# =============================================================================

# WHY THIS EXISTS
#
# Proxmox's left-hand tree is one flat list of machines per node, ordered by VM
# id. That is legible at five machines and stops being legible well before the
# estate stops growing, and the thing an operator actually wants to know when
# looking at it - is this one holding etcd, or is it a worker I can drain - is
# exactly what a flat list does not say.
#
# A pool is Proxmox's own answer, so this is not a scheme invented here.
#
# BY FUNCTION, NOT BY ANYTHING ELSE
#
# The grouping is what a machine is FOR. Not where it runs: the hypervisor
# already groups by node, and a second copy of that answer adds nothing. Not
# who owns it: there is one operator. Not lifecycle stage: the management tier
# has no staging and no production, and borrowing those words from the workload
# tier is the mistake CLAUDE.md names explicitly.
#
# Function is also the only axis that stays true as the estate grows. A machine
# can move between hypervisors, change size, be rebuilt on a new image - and
# remain a control plane throughout. The pool it belongs to should survive all
# of that, and this one does.

# The pools themselves, keyed by function.
#
# Declared as a map rather than three resource blocks so that adding a function
# is one entry rather than a copied block, and so the membership resources
# below can be written once against whichever pool their machines belong to.
#
# The comment reaches the Proxmox UI, which is the whole point of the exercise:
# somebody looking at the hypervisor should be told what the group is without
# having to come back here and read HCL.
locals {
  # site_name, not the map key, because a pool id is estate-facing. The
  # hypervisor console shows it, and the console is where somebody is trying to
  # work out what they are looking at - so it says the site's own name, exactly
  # as the VM names already do.
  #
  # Prefixed rather than bare, and that is a real decision rather than
  # decoration. Pools are datacenter-scoped objects: two sites sharing a
  # Proxmox cluster would collide on an unprefixed `control-plane`, and the
  # collision does not error - it silently adopts the other estate's machines
  # into this estate's pool. The config supports several sites by design, so
  # the collision is expressible, and this is the cheapest possible way to make
  # it impossible.
  pools = {
    control-plane = "Talos control planes: etcd quorum lives here. Draining one is a quorum decision."
    workers       = "Talos workers: CI and anything else that may be evicted and rescheduled."
    templates     = "Golden images, one per hypervisor. Not running machines - clone sources."
  }

  # Which machines belong to which function.
  #
  # Built from the same locals the VM resources iterate, so a machine cannot
  # exist without landing in a pool - there is no third list to keep in step.
  # The key is what makes each membership addressable in state; it carries the
  # function so two machines with the same id on different hypervisors, which
  # templates genuinely have, stay distinct.
  pool_members = merge(
    { for k, v in local.control_plane : "control-plane/${k}" => {
      pool  = "control-plane"
      vm_id = v.vm_id
    } },
    { for k, v in local.workers : "workers/${k}" => {
      pool  = "workers"
      vm_id = v.vm_id
    } },
    { for h in local.all_vm_hypervisors : "templates/${h}" => {
      pool  = "templates"
      vm_id = proxmox_virtual_environment_vm.talos_template[h].vm_id
    } },
  )
}

resource "proxmox_virtual_environment_pool" "by_function" {
  for_each = local.pools

  pool_id = "${local.site_name}-${each.key}"
  comment = each.value
}

# Membership as its own resource, rather than `pool_id` on each VM.
#
# THIS IS THE WHOLE REASON THIS CHANGE IS SAFE TO MAKE ON A LIVE ESTATE, and
# the epoch record's own note about it needs correcting rather than following.
# That note said pools had to be their own change because they "edit every
# existing VM". With `pool_id` set on the VM resource that is true - every
# machine gets an in-place update, including the three holding etcd. With a
# membership resource it is not true at all: no VM resource is touched, and the
# plan is five creates of a new kind of object beside them.
#
# It is also the only one of the two that is reversible. bpg/proxmox v0.111.1
# gives `pool_id` a DiffSuppressFunc that ignores the change when the new value
# is empty and the old one is not - so deleting the attribute from the config
# does nothing at all, and a machine can be put into a pool declaratively but
# never taken out of one. A membership resource removed from the config is
# destroyed, which is what a reader of the diff would expect to happen.
#
# `proxmox_pool_membership` rather than the older
# `proxmox_virtual_environment_pool_membership`: the latter is deprecated in
# this provider version and is removed in v1.0.
#
# Both need Pool.Allocate on the pool path, which the TerraformProv role did
# not carry until the same change that added this file - see
# management/hypervisor/hypervisor-prep.yml, where the reason it could not
# simply be added to a list is written down.
resource "proxmox_pool_membership" "by_function" {
  for_each = local.pool_members

  pool_id = proxmox_virtual_environment_pool.by_function[each.value.pool].pool_id
  vm_id   = each.value.vm_id
}
