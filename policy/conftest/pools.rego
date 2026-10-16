# A machine's place in a pool is legible in a plan, and comes out before the
# machine does.
#
# That every machine class is placed, and that a pool's id carries its site,
# are about what the cluster module produces. They stay Go guards until they
# move down to the module's own tests.
package pools

import rego.v1

memberships := [[name, block] |
	some f in input
	endswith(f.path, ".tf")
	some name, blocks in object.get(object.get(f.contents, "resource", {}), "proxmox_pool_membership", {})
	some block in blocks
]

# The function belongs in the resource NAME, not in a for_each key.
#
# This is a legibility guard with a security reason underneath it. redactKeys
# strips any for_each key that is not purely numeric, because a key can be a
# hypervisor's real hostname - a vault value - and nothing downstream can tell
# a safe key from an unsafe one. So a membership keyed by function rendered as
# nine identical `<redacted>` rows in the plan comment, on a change whose
# entire purpose is that somebody can see what is what.
#
# A resource name cannot carry a vault value: it is an HCL identifier written
# in this file. Keeping the function there is what makes the plan readable
# without weakening redaction, and this fails if somebody consolidates the
# blocks back into one resource over a map of functions.

deny contains msg if {
	count(memberships) < 2
	msg := sprintf("there are %d proxmox_pool_membership resources, so at most one function is named in a resource name. Consolidating these into one resource over a map of functions puts the function into a for_each key, and redactKeys cannot tell that key from a hypervisor hostname - so every row in the plan comment becomes \"<redacted>\" and the change that exists to make the estate legible produces a plan nobody can read.", [count(memberships)])
}

deny contains msg if {
	some [name, _] in memberships
	contains(name, "by_function")
	msg := sprintf("proxmox_pool_membership.%s is named for the grouping rather than for a function. The plan renders the resource name and redacts the key, so the name is the only part a reviewer can read. It has to say which pool this is.", [name])
}

# A pool membership must depend on the machine it places, and the only way to
# say that in HCL is to read the vm_id off the VM resource.
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
# `each.value.vm_id` reads the same number from a local and creates no edge at
# all, which is why it cannot be spelled that way. The number being identical
# is exactly what makes this easy to get wrong and impossible to see in review.

deny contains msg if {
	some [name, block] in memberships
	not block.vm_id
	msg := sprintf("proxmox_pool_membership %q sets no vm_id", [name])
}

deny contains msg if {
	some [name, block] in memberships
	not regex.match(`^\$\{proxmox_virtual_environment_vm\.[A-Za-z0-9_]+\[[^\]]+\]\.vm_id\}$`, block.vm_id)
	msg := sprintf("proxmox_pool_membership %q takes vm_id from %s. It must read vm_id off the VM resource itself, so that OpenTofu orders the membership's destroy before the machine's. Reading the same number out of a local creates no dependency edge, the two deletes race, and the one that loses asks Proxmox to remove a VM that is already gone from the pool - HTTP 500, and a teardown that stops half-done.", [name, block.vm_id])
}
