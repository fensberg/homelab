# The cluster-health gate is read where the machines are built, and depends
# on the machines it is asking about.
#
# WHERE. The platform root is applied only after the cluster root's last
# step has returned, and the gate is what makes "returned" mean "every node
# is Ready". Read anywhere but beside the machines, nothing waits for the
# nodes before the platform is put on them.
#
# WHAT IT DEPENDS ON. It is scoped to the nodes the config asks for, which is
# fully known at plan time. Without a dependency on the VMs, a plan that
# changes the machine count reads against addresses with no machine behind
# them, waits, and times out after ten minutes producing nothing; the same
# scoping made a destroy ask whether a three-node cluster was a healthy
# five-node one and spend ten minutes finding out (#117). OpenTofu defers a
# data source read to apply time when something it depends on has pending
# changes, so depending on the VMs is what makes this a gate rather than an
# obstruction.
#
# ON THE PLAN'S CONFIGURATION: a plan made from nothing is made offline and
# reads no health, so the gate is judged as it is declared.
package rules.plan_health_gate

import data.lib
import rego.v1

machines := "proxmox_virtual_environment_vm.talos_cp"

gates contains [path, address, r] if {
	some [path, address, r] in lib.plan_configuration
	r.mode == "data"
	r.type == "talos_cluster_health"
}

builds_machines contains path if {
	some [path, _, r] in lib.plan_configuration
	r.type == "proxmox_virtual_environment_vm"
}

deny contains msg if {
	count(gates) == 0
	msg := "no data \"talos_cluster_health\" is in any plan's configuration, so there is no gate to depend on the machines"
}

deny contains msg if {
	some [path, address, _] in gates
	not path in builds_machines
	msg := sprintf("%s is read by a root that builds no machines, so it is applied after the root that builds them has returned and nothing waits for the nodes before the platform is put on them", [address])
}

deny contains msg if {
	some [_, address, r] in gates
	not machines in object.get(r, "depends_on", [])
	msg := sprintf("%s does not depend on %s. Without it the read happens at plan time against nodes the config asks for but that do not exist, so a plan that changes the machine count times out after ten minutes and a destroy does the same. See #117.", [address, machines])
}
