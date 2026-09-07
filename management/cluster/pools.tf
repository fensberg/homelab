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
#
# WHY THIS IS SIX RESOURCES RATHER THAN TWO OVER A MAP OF FUNCTIONS
#
# The first version was two resources with `for_each` over a map keyed by
# function, which is tidier HCL and one edit to add a function. The plan comment
# it produced was nine identical lines:
#
#   add  proxmox_pool_membership.by_function["<redacted>"]
#   add  proxmox_pool_membership.by_function["<redacted>"]
#   ...
#
# because redactKeys strips any `for_each` key that is not purely numeric, and
# it is right to: a key can be a hypervisor's real hostname, which is a vault
# value, and nothing downstream can tell a safe key from an unsafe one. So the
# tidier form produced a plan that told a reviewer nothing about a change whose
# entire purpose is legibility.
#
# A RESOURCE NAME IS PUBLIC BY CONSTRUCTION - it is an HCL identifier, written
# in this file, and cannot carry a value from the vault. Putting the function
# there rather than in the key means the plan reads:
#
#   add  proxmox_pool_membership.control_plane["100"]
#   add  proxmox_pool_membership.templates["<redacted>"]
#
# The control planes and workers are keyed by host octet, which is numeric and
# survives redaction; the templates are keyed by hypervisor and are correctly
# redacted, which is the one place a name genuinely is a secret. Repetition is
# the cost, and it buys a plan somebody can read.

# --- the pools ---------------------------------------------------------------
#
# pool_id carries the site name, and that is a real decision rather than
# decoration. Pools are datacenter-scoped objects: two sites sharing a Proxmox
# cluster would collide on an unprefixed `control-plane`, and the collision does
# not error - Proxmox silently adopts the other estate's machines into this
# estate's pool. The config supports several sites by design, so the collision
# is expressible, and a prefix is the cheapest way to make it impossible.
#
# site_name rather than the site key, because a pool id is estate-facing: the
# hypervisor console shows it, and the console is where somebody is working out
# what they are looking at. The VM names already do the same.
#
# The comment reaches that console too, which is the whole point of the
# exercise - somebody looking at the tree should be told what a group is
# without coming back here to read HCL.

resource "proxmox_virtual_environment_pool" "control_plane" {
  pool_id = "${local.site_name}-control-plane"
  comment = "Talos control planes. etcd quorum lives here, so draining one is a quorum decision."
}

resource "proxmox_virtual_environment_pool" "workers" {
  pool_id = "${local.site_name}-workers"
  comment = "Talos workers. CI and anything else that may be evicted and rescheduled."
}

resource "proxmox_virtual_environment_pool" "templates" {
  pool_id = "${local.site_name}-templates"
  comment = "Golden images, one per hypervisor. Clone sources rather than running machines."
}

# --- membership --------------------------------------------------------------
#
# Membership as its own resource, rather than `pool_id` on each VM.
#
# THIS IS WHY THIS CHANGE IS SAFE TO MAKE ON A LIVE ESTATE, and the epoch
# record's own note about it needed correcting rather than following. That note
# said pools had to be their own change because they "edit every existing VM".
# With `pool_id` set on the VM resource that is true - every machine gets an
# in-place update, including the three holding etcd. With a membership resource
# it is not true at all: no VM resource is touched, and the plan is creates of a
# new kind of object beside them.
#
# It is also the only one of the two that is reversible. bpg/proxmox v0.111.1
# gives `pool_id` a DiffSuppressFunc that ignores the change when the new value
# is empty and the old one is not - so deleting the attribute from the config
# does nothing at all, and a machine can be put into a pool declaratively but
# never taken out of one. A membership resource removed from the config is
# destroyed, which is what a reader of the diff would expect.
#
# `proxmox_pool_membership` rather than the older
# `proxmox_virtual_environment_pool_membership`: the latter is deprecated in
# this provider version and is removed in v1.0.
#
# All of these need Pool.Allocate on the pool path, which the TerraformProv role
# did not carry until the same change that added this file - see
# management/hypervisor/hypervisor-prep.yml, where the reason it could not
# simply be added to a list is written down.
#
# Each iterates the same local its machines' VM resource iterates, so a machine
# cannot exist without landing in a pool: there is no third list to keep in
# step, and tests/go/repo/pools_test.go fails if a VM resource ever iterates
# something no membership resource does.

resource "proxmox_pool_membership" "control_plane" {
  for_each = local.control_plane

  pool_id = proxmox_virtual_environment_pool.control_plane.pool_id
  vm_id   = each.value.vm_id
}

resource "proxmox_pool_membership" "workers" {
  for_each = local.workers

  pool_id = proxmox_virtual_environment_pool.workers.pool_id
  vm_id   = each.value.vm_id
}

resource "proxmox_pool_membership" "templates" {
  for_each = toset(local.all_vm_hypervisors)

  pool_id = proxmox_virtual_environment_pool.templates.pool_id
  vm_id   = proxmox_virtual_environment_vm.talos_template[each.key].vm_id
}
