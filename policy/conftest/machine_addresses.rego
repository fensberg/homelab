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
# A KIND, NOT A LIST. Every machine the hypervisor's provider is asked to
# make, wherever it is declared, and not the kinds there were when this was
# written. A template is not a machine: it is never started.
package rules.machine_addresses

import data.lib
import rego.v1

machine_type := "proxmox_virtual_environment_vm"

# machines is every machine the OpenTofu asks the hypervisor to make:
# [file, name, block], a template left out.
machines contains [f.path, name, block] if {
	some f in lib.opentofu
	some name, blocks in object.get(object.get(f.contents, "resource", {}), machine_type, {})
	some block in blocks
	object.get(block, "template", false) != true
}

# addressed is a machine given an address of its own: an initialization
# holding an ip_config whose address family sets one.
addressed(block) if {
	some init in object.get(block, "initialization", [])
	some config in object.get(init, "ip_config", [])
	some _, families in config
	some family in families
	is_string(family.address)
}

deny contains msg if {
	count(machines) == 0
	msg := sprintf("no %s that is a machine was found, so this checked nothing", [machine_type])
}

deny contains msg if {
	some [path, name, block] in machines
	not addressed(block)
	msg := sprintf("%s: %s.%s is built with no address of its own. It boots with nothing, on a network that hands nothing out, and its configuration cannot reach it: give it an initialization block with an ip_config, on the subnet it is on.", [path, machine_type, name])
}
