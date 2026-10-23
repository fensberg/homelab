package rules.machine_addresses_test

import data.rules.machine_addresses
import rego.v1

vm(name, block) := {"path": "made-up.tf", "contents": {"resource": {"proxmox_virtual_environment_vm": {name: [block]}}}}

addressed := vm("addressed", {"initialization": [{"ip_config": [{"ipv4": [{"address": "${each.value.ip}/24"}]}]}]})

denies(i, words) if {
	some msg in machine_addresses.deny with input as i
	contains(msg, words)
}

test_an_addressed_machine_passes if {
	count(machine_addresses.deny) == 0 with input as [addressed]
}

test_a_machine_with_nothing_is_refused if {
	denies([addressed, vm("bare", {"name": "x"})], "proxmox_virtual_environment_vm.bare is built with no address")
}

test_a_machine_told_only_its_resolvers_is_refused if {
	denies([addressed, vm("resolvers", {"initialization": [{"dns": [{"servers": ["192.0.2.1"]}]}]})], "proxmox_virtual_environment_vm.resolvers is built with no address")
}

test_a_template_is_not_a_machine if {
	count(machine_addresses.deny) == 0 with input as [addressed, vm("a_template", {"template": true})]
}

test_another_kind_of_resource_is_not_a_machine if {
	count(machine_addresses.deny) == 0 with input as [addressed, {"path": "other.tf", "contents": {"resource": {"something_else": {"ignored": [{}]}}}}]
}

test_no_machine_at_all_is_refused if {
	denies([{"path": "other.tf", "contents": {"resource": {}}}], "checked nothing")
}
