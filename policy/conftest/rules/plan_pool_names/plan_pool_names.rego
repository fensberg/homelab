# A machine's place in a pool is legible in a plan: the function belongs in
# the resource NAME, not in a for_each key.
#
# This is a legibility guard with a security reason underneath it. redactKeys
# strips any for_each key that is not purely numeric, because a key can be a
# hypervisor's real hostname - a vault value - and nothing downstream can tell
# a safe key from an unsafe one. So a membership keyed by function rendered as
# nine identical `<redacted>` rows in the plan comment, on a change whose
# entire purpose is that somebody can see what is what.
#
# A resource name cannot carry a vault value: it is an HCL identifier.
# Keeping the function there is what makes the plan readable without
# weakening redaction, and this fails if somebody consolidates the blocks
# back into one resource over a map of functions.
#
# ON THE PLAN'S CONFIGURATION: each membership as it is declared.
package rules.plan_pool_names

import data.lib
import rego.v1

memberships contains [address, r] if {
	some [_, address, r] in lib.plan_configuration
	r.type == "proxmox_pool_membership"
}

deny contains msg if {
	count(memberships) < 2
	msg := $"there are {count(memberships)} proxmox_pool_membership resources, so at most one function is named in a resource name. Consolidating these into one resource over a map of functions puts the function into a for_each key, and redactKeys cannot tell that key from a hypervisor hostname - so every row in the plan comment becomes \"<redacted>\" and the change that exists to make the estate legible produces a plan nobody can read."
}

deny contains msg if {
	some [address, r] in memberships
	contains(r.name, "by_function")
	msg := $"{address} is named for the grouping rather than for a function. The plan renders the resource name and redacts the key, so the name is the only part a reviewer can read. It has to say which pool this is."
}
