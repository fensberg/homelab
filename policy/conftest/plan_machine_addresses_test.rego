package rules.plan_machine_addresses_test

import data.rules.plan_machine_addresses
import rego.v1

plan(resources) := {"path": "site-cluster.plan.json", "contents": {"planned_values": {"root_module": {"child_modules": [{"resources": resources}]}}}}

vm(address, values) := {"address": address, "type": "proxmox_virtual_environment_vm", "values": values}

addressed := vm("module.cluster.proxmox_virtual_environment_vm.worker[\"200\"]", {"initialization": [{"ip_config": [{"ipv4": [{"address": "192.0.2.10/24"}]}]}]})

denies(i, words) if {
	some msg in plan_machine_addresses.deny with input as i
	contains(msg, words)
}

test_an_addressed_machine_passes if {
	count(plan_machine_addresses.deny) == 0 with input as [plan([addressed])]
}

test_a_machine_with_nothing_is_refused if {
	denies([plan([addressed, vm("module.cluster.proxmox_virtual_environment_vm.bare[\"1\"]", {"name": "x"})])], "proxmox_virtual_environment_vm.bare[\"1\"] is built with no address")
}

test_a_machine_told_only_its_resolvers_is_refused if {
	denies([plan([addressed, vm("module.cluster.proxmox_virtual_environment_vm.resolvers", {"initialization": [{"dns": [{"servers": ["192.0.2.1"]}]}]})])], "proxmox_virtual_environment_vm.resolvers is built with no address")
}

test_a_template_is_not_a_machine if {
	count(plan_machine_addresses.deny) == 0 with input as [plan([addressed, vm("module.cluster.proxmox_virtual_environment_vm.a_template", {"template": true})])]
}

test_another_kind_of_resource_is_not_a_machine if {
	count(plan_machine_addresses.deny) == 0 with input as [plan([addressed, {"address": "module.cluster.something_else.ignored", "type": "something_else", "values": {}}])]
}

test_a_machine_in_the_source_and_not_in_a_plan_is_not_judged_here if {
	denies([{"path": "made-up.tf", "contents": {"resource": {"proxmox_virtual_environment_vm": {"bare": [{"name": "x"}]}}}}], "checked nothing")
}

test_no_machine_at_all_is_refused if {
	denies([plan([])], "checked nothing")
}
