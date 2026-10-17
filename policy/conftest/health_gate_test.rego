package rules.health_gate_test

import data.rules.health_gate
import rego.v1

gate(deps) := [{"path": "modules/x/talos.tf", "contents": {"data": {"talos_cluster_health": {"this": [{"control_plane_nodes": "${local.node_ips}", "depends_on": deps}]}}}}]

test_a_gate_on_the_machines_passes if {
	count(health_gate.deny) == 0 with input as gate(["${talos_cluster_kubeconfig.this}", "${proxmox_virtual_environment_vm.talos_cp}"])
}

test_a_gate_not_on_the_machines_is_refused if {
	some msg in health_gate.deny with input as gate(["${talos_cluster_kubeconfig.this}"])
	contains(msg, "does not depend on proxmox_virtual_environment_vm.talos_cp")
}

test_a_gate_naming_the_machines_elsewhere_is_still_refused if {
	i := [{"path": "modules/x/talos.tf", "contents": {"data": {"talos_cluster_health": {"this": [{"endpoints": "${proxmox_virtual_environment_vm.talos_cp[*].id}"}]}}}}]
	some msg in health_gate.deny with input as i
	contains(msg, "does not depend on")
}

test_no_gate_is_refused if {
	some msg in health_gate.deny with input as []
	contains(msg, "no data \"talos_cluster_health\"")
}
