# Every machine a site builds lands in a pool named for what it is for.
#
# WHAT THIS GUARDS, which is not tidiness. The pools exist so that somebody
# looking at the hypervisor can tell a control plane from a worker without
# coming back here to read HCL. That property does not break by someone
# deleting a pool - that is a visible, deliberate change. It breaks by
# ADDITION: a new class of machine arrives, its resource block is written
# beside the others, and nothing anywhere says it belongs in a pool. The
# hypervisor grows an ungrouped machine and the grouping quietly stops
# meaning "all of them".
#
# ON THE PLAN. Every machine the plan would make, each instance of each, is
# matched to a membership by the id the hypervisor knows it by, so a class
# placed by iterating the wrong collection is seen as the machines it left
# out.
package rules.plan_pool_membership

import data.lib
import rego.v1

machines contains r if {
	some [_, r] in lib.plan
	r.type == "proxmox_virtual_environment_vm"
}

placed contains r.values.vm_id if {
	some [_, r] in lib.plan
	r.type == "proxmox_pool_membership"
}

deny contains msg if {
	count(machines) == 0
	msg := "no machine is in any plan, so this checked nothing"
}

deny contains msg if {
	count(machines) > 0
	count(placed) == 0
	msg := "no plan places any machine in a pool: no proxmox_pool_membership is planned"
}

deny contains msg if {
	some r in machines
	not r.values.vm_id in placed
	msg := $"{r.address} is built but never placed in a pool. Add a proxmox_pool_membership iterating the collection it is made from, in the pool for the function it serves: the hypervisor groups machines by what they are for, and a machine that is missing does not break that visibly - it just quietly stops being true of everything."
}
