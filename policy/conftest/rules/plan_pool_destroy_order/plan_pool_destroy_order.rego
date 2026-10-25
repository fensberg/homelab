# A pool membership comes out before the machine it places, because it
# depends on that machine.
#
# WHAT THIS GUARDS. On destroy, OpenTofu reverses the dependency graph - so an
# edge from the membership to the VM is what makes the membership come out
# first, while the VM still exists to be removed from a pool. Without that
# edge the two are unordered, they are deleted concurrently, and deleting the
# VM takes it out of its pool as a side effect. The membership delete that
# loses the race then asks Proxmox to remove a VM that is no longer a member,
# which answers HTTP 500 and fails the teardown.
#
# That is not hypothetical. It happened twice in one session, on
# `templates["<hypervisor>"]`, `control_plane["102"]`, `workers["200"]` and
# `workers["202"]`, and each time it left the estate half-destroyed with state
# deliberately preserved - the "unexploded ordnance" case the destroy path
# exists to avoid.
#
# `each.value.vm_id` reads the same number from a local and creates no edge
# at all, which is why it cannot be spelled that way. The number being
# identical is exactly what makes this easy to get wrong and impossible to
# see in review.
#
# ON THE PLAN'S CONFIGURATION: what each membership's vm_id is made from.
# Read off the machine, it refers to the machine's resource.
package rules.plan_pool_destroy_order

import data.lib
import rego.v1

memberships contains [address, r] if {
	some [_, address, r] in lib.plan_configuration
	r.type == "proxmox_pool_membership"
}

off_the_machine(r) if {
	some ref in object.get(object.get(r.expressions, "vm_id", {}), "references", [])
	startswith(ref, "proxmox_virtual_environment_vm.")
}

deny contains msg if {
	count(memberships) == 0
	msg := "no proxmox_pool_membership is in any plan's configuration, so this checked nothing"
}

deny contains msg if {
	some [address, r] in memberships
	not off_the_machine(r)
	msg := sprintf("%s does not read its vm_id off the VM resource. It must, so that OpenTofu orders the membership's destroy before the machine's. Reading the same number out of a local creates no dependency edge, the two deletes race, and the one that loses asks Proxmox to remove a VM that is already gone from the pool - HTTP 500, and a teardown that stops half-done.", [address])
}
