# Every machine a site builds is given its address when it is built.
#
# WHY. A machine boots with no configuration, and its configuration is sent
# to it over the network. So it needs an address before it has been told
# anything, and on these networks nothing hands one out: it is given with
# the machine, by the hypervisor. A machine built without one comes up with
# nothing, and the converge that built it waits for an API that nothing can
# reach. The first machine of an untrusted zone did exactly that, because
# the zone's was the one kind of machine written without it.
#
# ON THE PLAN, NOT THE SOURCE. Every machine the plan would make, each
# instance of each, with the values OpenTofu worked out for it: a machine
# made by a for_each, in a module, or from a local is judged as it would be
# built, wherever and however it is written. A template is not a machine: it
# is never started.
package rules.plan_machine_addresses

import data.lib
import rego.v1

machine_type := "proxmox_virtual_environment_vm"

# machines is every machine a plan would make: [plan, resource], a template
# left out.
machines contains [path, r] if {
	some [path, r] in lib.plan
	r.type == machine_type
	object.get(r.values, "template", false) != true
}

# addressed is a machine given an address of its own: an initialization
# holding an ip_config whose address family sets one.
addressed(r) if {
	some init in object.get(r.values, "initialization", [])
	some config in object.get(init, "ip_config", [])
	some families in config
	some family in families
	is_string(family.address)
}

deny contains msg if {
	count(machines) == 0
	msg := sprintf("no %s that is a machine is in any plan, so this checked nothing", [machine_type])
}

deny contains msg if {
	some [_, r] in machines
	not addressed(r)
	msg := sprintf("%s is built with no address of its own. It boots with nothing, on a network that hands nothing out, and its configuration cannot reach it: give it an initialization block with an ip_config, on the subnet it is on.", [r.address])
}
