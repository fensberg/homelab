# The cluster-health gate must depend on the machines it is asking about.
#
# It is scoped to `local.node_ips` - the nodes the config asks for - which is
# fully known at plan time. Without a dependency on the VMs, a plan that
# changes the machine count reads against addresses with no machine behind
# them, waits, and times out after ten minutes producing nothing. The same
# scoping made a destroy ask whether a three-node cluster was a healthy
# five-node one and spend ten minutes finding out.
#
# OpenTofu defers a data source read to apply time when something it depends
# on has pending changes, so depending on the VMs is what makes this a gate
# rather than an obstruction. Removing that dependency silently reintroduces
# both failures, and neither is visible until somebody changes the node count
# or tears an estate down - which is to say, at the two worst moments.
package health_gate

import rego.v1

machines := "proxmox_virtual_environment_vm.talos_cp"

gates contains [f.path, name, block] if {
	some f in input
	endswith(f.path, ".tf")
	some name, blocks in object.get(object.get(f.contents, "data", {}), "talos_cluster_health", {})
	some block in blocks
}

deny contains msg if {
	count(gates) == 0
	msg := "no data \"talos_cluster_health\" is declared, so there is no gate to depend on the machines"
}

deny contains msg if {
	some [path, name, block] in gates
	not sprintf("${%s}", [machines]) in object.get(block, "depends_on", [])
	msg := sprintf("data.talos_cluster_health.%s in %s does not depend on %s. Without it the read happens at plan time against nodes the config asks for but that do not exist, so a plan that changes the machine count times out after ten minutes and a destroy does the same. See #117.", [name, path, machines])
}
