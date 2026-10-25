package rules.plan_one_machine_list_test

import data.rules.plan_one_machine_list
import rego.v1

configured(resources) := {"path": "site-cluster.plan.json", "contents": {"configuration": {"root_module": {"module_calls": {"cluster": {"module": {"resources": resources}}}}}}}

kind(name, patches) := {"address": sprintf("data.talos_machine_configuration.%s", [name]), "mode": "data", "type": "talos_machine_configuration", "expressions": {"config_patches": patches}}

denies(i, words) if {
	some msg in plan_one_machine_list.deny with input as i
	contains(msg, words)
}

test_every_kind_from_the_list_passes if {
	count(plan_one_machine_list.deny) == 0 with input as [configured([kind("worker", {"references": ["local.cluster_network_patch", "local.machine_patches", "each.key"]}), kind("dmz", {"references": ["local.machine_patches"]})])]
}

test_a_list_of_its_own_is_refused if {
	denies([configured([kind("worker", {"references": ["local.machine_patches"]}), kind("dmz", {"references": ["local.dmz_patches"]})])], "data.talos_machine_configuration.dmz is not configured from local.machine_patches")
}

test_patches_written_out_are_refused if {
	denies([configured([kind("dmz", {"constant_value": ["machine: {}"]})])], "is not configured from local.machine_patches")
}

test_a_variable_so_named_is_refused if {
	denies([configured([kind("dmz", {"references": ["var.machine_patches"]})])], "is not configured from")
}

test_no_kind_is_refused if {
	denies([configured([])], "checked nothing")
}
