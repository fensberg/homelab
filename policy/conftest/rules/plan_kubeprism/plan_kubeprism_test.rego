package rules.plan_kubeprism_test

import data.rules.plan_kubeprism
import rego.v1

plan(configs) := {"path": "site-cluster.plan.json", "contents": {"planned_values": {"root_module": {"child_modules": [{"resources": configs}]}}}}

values(port) := {"path": "clusters/bootstrap/cilium-values.yaml", "contents": {"k8sServiceHost": "localhost", "k8sServicePort": port}}

machine(address, patches) := {"address": address, "type": "talos_machine_configuration", "mode": "data", "values": {"config_patches": patches}}

prism(port) := $"machine:\n  features:\n    kubePrism:\n      enabled: true\n      port: {port}\n"

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_kubeprism_on_the_cnis_port_passes if {
	count(plan_kubeprism.deny) == 0 with input as [values(7445), plan([machine("m[\"100\"]", [prism(7445), null])])]
}

test_a_machine_without_it_is_refused if {
	denies(plan_kubeprism.deny, "w[\"200\"] never declares KubePrism") with input as [values(7445), plan([machine("m[\"100\"]", [prism(7445)]), machine("w[\"200\"]", ["machine:\n  install:\n    disk: /dev/sda\n"])])]
}

test_kubeprism_switched_off_is_refused if {
	denies(plan_kubeprism.deny, "does not enable it") with input as [values(7445), plan([machine("m[\"100\"]", ["machine:\n  features:\n    kubePrism:\n      enabled: false\n      port: 7445\n"])])]
}

test_a_port_the_cni_does_not_use_is_refused if {
	denies(plan_kubeprism.deny, "declares KubePrism on port 7446, and the CNI's values point k8sServicePort at 7445") with input as [values(7445), plan([machine("m[\"100\"]", [prism(7446)])])]
}

test_the_cni_moving_alone_is_refused if {
	denies(plan_kubeprism.deny, "declares KubePrism on port 7445, and the CNI's values point k8sServicePort at 7446") with input as [values(7446), plan([machine("m[\"100\"]", [prism(7445)])])]
}

test_no_cni_values_is_refused if {
	denies(plan_kubeprism.deny, "name 0 k8sServicePort") with input as [plan([machine("m[\"100\"]", [prism(7445)])])]
}

test_no_machine_configuration_is_refused if {
	denies(plan_kubeprism.deny, "checked nothing") with input as [values(7445), plan([])]
}
