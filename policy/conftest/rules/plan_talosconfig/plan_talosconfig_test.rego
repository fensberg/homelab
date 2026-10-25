package rules.plan_talosconfig_test

import data.rules.plan_talosconfig
import rego.v1

plan(resources) := {"path": "site-cluster.plan.json", "contents": {"planned_values": {"root_module": {"child_modules": [{"resources": resources}]}}}}

credential(nodes, endpoints) := {"address": "data.talos_client_configuration.this", "type": "talos_client_configuration", "values": {"nodes": nodes, "endpoints": endpoints}}

cp(n) := {"address": sprintf("data.talos_machine_configuration.controlplane[\"%d\"]", [n]), "type": "talos_machine_configuration", "values": {"machine_type": "controlplane"}}

worker := {"address": "data.talos_machine_configuration.worker[\"200\"]", "type": "talos_machine_configuration", "values": {"machine_type": "worker"}}

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_nodes_and_every_control_plane_pass if {
	count(plan_talosconfig.deny) == 0 with input as [plan([cp(100), cp(101), worker, credential(["a", "b"], ["a", "b"])])]
}

test_no_nodes_is_refused if {
	denies(plan_talosconfig.deny, "renders a talosconfig that sets no nodes") with input as [plan([cp(100), credential([], ["a"])])]
}

test_one_endpoint_of_several_is_refused if {
	denies(plan_talosconfig.deny, "with 1 endpoint(s), and the site has 3 control plane(s)") with input as [plan([cp(100), cp(101), cp(102), credential(["a", "b", "c"], ["a"])])]
}

test_no_credential_is_refused if {
	denies(plan_talosconfig.deny, "checked nothing") with input as [plan([cp(100)])]
}
