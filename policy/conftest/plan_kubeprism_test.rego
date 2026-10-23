package rules.plan_kubeprism_test

import data.rules.plan_kubeprism
import rego.v1

plan(configs) := {"path": "site-cluster.plan.json", "contents": {"planned_values": {"root_module": {"child_modules": [{"resources": configs}]}}}}

values(port) := {"path": "clusters/bootstrap/cilium-values.yaml", "contents": {"k8sServiceHost": "localhost", "k8sServicePort": port}}

machine(address, patches) := {"address": address, "type": "talos_machine_configuration", "mode": "data", "values": {"config_patches": patches}}

prism(port) := sprintf("machine:\n  features:\n    kubePrism:\n      enabled: true\n      port: %d\n", [port])

denies(i, words) if {
	some msg in plan_kubeprism.deny with input as i
	contains(msg, words)
}

test_kubeprism_on_the_cnis_port_passes if {
	count(plan_kubeprism.deny) == 0 with input as [values(7445), plan([machine("m[\"100\"]", [prism(7445), null])])]
}

test_a_machine_without_it_is_refused if {
	denies([values(7445), plan([machine("m[\"100\"]", [prism(7445)]), machine("w[\"200\"]", ["machine:\n  install:\n    disk: /dev/sda\n"])])], "w[\"200\"] never declares KubePrism")
}

test_kubeprism_switched_off_is_refused if {
	denies([values(7445), plan([machine("m[\"100\"]", ["machine:\n  features:\n    kubePrism:\n      enabled: false\n      port: 7445\n"])])], "does not enable it")
}

test_a_port_the_cni_does_not_use_is_refused if {
	denies([values(7445), plan([machine("m[\"100\"]", [prism(7446)])])], "declares KubePrism on port 7446, and the CNI's values point k8sServicePort at 7445")
}

test_the_cni_moving_alone_is_refused if {
	denies([values(7446), plan([machine("m[\"100\"]", [prism(7445)])])], "declares KubePrism on port 7445, and the CNI's values point k8sServicePort at 7446")
}

test_no_cni_values_is_refused if {
	denies([plan([machine("m[\"100\"]", [prism(7445)])])], "name 0 k8sServicePort")
}

test_no_machine_configuration_is_refused if {
	denies([values(7445), plan([])], "checked nothing")
}
