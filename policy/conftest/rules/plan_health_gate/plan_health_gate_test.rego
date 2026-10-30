package rules.plan_health_gate_test

import data.rules.plan_health_gate
import rego.v1

configured(path, resources) := {"path": path, "contents": {"configuration": {"root_module": {"module_calls": {"cluster": {"module": {"resources": resources}}}}}}}

vm := {"address": "proxmox_virtual_environment_vm.talos_cp", "mode": "managed", "type": "proxmox_virtual_environment_vm", "expressions": {}}

gate(deps) := {"address": "data.talos_cluster_health.this", "mode": "data", "type": "talos_cluster_health", "expressions": {}, "depends_on": deps}

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_a_gate_on_the_machines_beside_them_passes if {
	count(plan_health_gate.deny) == 0 with input as [configured("c.plan.json", [vm, gate(["talos_cluster_kubeconfig.this", "proxmox_virtual_environment_vm.talos_cp", "terraform_data.cilium"])])]
}

test_a_gate_not_on_the_machines_is_refused if {
	denies(plan_health_gate.deny, "module.cluster.data.talos_cluster_health.this does not depend on proxmox_virtual_environment_vm.talos_cp") with input as [configured("c.plan.json", [vm, gate(["talos_cluster_kubeconfig.this"])])]
}

test_a_gate_in_a_root_without_machines_is_refused if {
	denies(plan_health_gate.deny, "is read by a root that builds no machines") with input as [configured("c.plan.json", [vm]), configured("p.plan.json", [gate(["proxmox_virtual_environment_vm.talos_cp", "terraform_data.cilium"])])]
}

test_no_gate_is_refused if {
	denies(plan_health_gate.deny, "no gate to depend on the machines") with input as [configured("c.plan.json", [vm])]
}

test_a_gate_not_on_the_cni_is_refused if {
	denies(plan_health_gate.deny, "does not depend on terraform_data.cilium") with input as [configured("c.plan.json", [vm, gate(["proxmox_virtual_environment_vm.talos_cp"])])]
}
