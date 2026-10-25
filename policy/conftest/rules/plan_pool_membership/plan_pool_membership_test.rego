package rules.plan_pool_membership_test

import data.rules.plan_pool_membership
import rego.v1

plan(resources) := {"path": "site-cluster.plan.json", "contents": {"planned_values": {"root_module": {"child_modules": [{"resources": resources}]}}}}

vm(address, id) := {"address": address, "type": "proxmox_virtual_environment_vm", "values": {"vm_id": id}}

member(address, id) := {"address": address, "type": "proxmox_pool_membership", "values": {"vm_id": id, "pool_id": "a-pool"}}

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_every_machine_placed_passes if {
	count(plan_pool_membership.deny) == 0 with input as [plan([vm("vm.a[\"1\"]", 1), member("m.a[\"1\"]", 1), vm("vm.b[\"2\"]", 2), member("m.b[\"2\"]", 2)])]
}

test_a_machine_left_out_is_refused if {
	denies(plan_pool_membership.deny, "vm.b[\"2\"] is built but never placed in a pool") with input as [plan([vm("vm.a[\"1\"]", 1), member("m.a[\"1\"]", 1), vm("vm.b[\"2\"]", 2)])]
}

test_a_membership_of_another_machine_does_not_place_this_one if {
	denies(plan_pool_membership.deny, "never placed in a pool") with input as [plan([vm("vm.a[\"1\"]", 1), member("m.a[\"1\"]", 1), member("m.a[\"3\"]", 3), vm("vm.b[\"2\"]", 2)])]
}

test_no_membership_at_all_is_refused if {
	denies(plan_pool_membership.deny, "no proxmox_pool_membership is planned") with input as [plan([vm("vm.a[\"1\"]", 1)])]
}

test_no_machine_at_all_is_refused if {
	denies(plan_pool_membership.deny, "checked nothing") with input as [plan([])]
}
