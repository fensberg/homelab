package rules.plan_pool_destroy_order_test

import data.rules.plan_pool_destroy_order
import rego.v1

configured(resources) := {"path": "site-cluster.plan.json", "contents": {"configuration": {"root_module": {"module_calls": {"cluster": {"module": {"resources": resources}}}}}}}

member(name, refs) := {"address": $"proxmox_pool_membership.{name}", "mode": "managed", "type": "proxmox_pool_membership", "expressions": {"vm_id": {"references": refs}}}

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_vm_id_off_the_machine_passes if {
	count(plan_pool_destroy_order.deny) == 0 with input as [configured([member("workers", ["proxmox_virtual_environment_vm.talos_worker", "each.key"])])]
}

test_vm_id_from_a_local_is_refused if {
	denies(plan_pool_destroy_order.deny, "module.cluster.proxmox_pool_membership.workers does not read its vm_id off the VM resource") with input as [configured([member("workers", ["each.value.vm_id", "each.value"])])]
}

test_no_vm_id_is_refused if {
	denies(plan_pool_destroy_order.deny, "does not read its vm_id off the VM resource") with input as [configured([{"address": "proxmox_pool_membership.w", "mode": "managed", "type": "proxmox_pool_membership", "expressions": {}}])]
}

test_no_membership_is_refused if {
	denies(plan_pool_destroy_order.deny, "checked nothing") with input as [configured([])]
}
