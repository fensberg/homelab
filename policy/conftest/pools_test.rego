package pools_test

import data.pools
import rego.v1

compute := {"path": "modules/c/compute.tf", "contents": {"resource": {"proxmox_virtual_environment_vm": {
	"talos_cp": [{"for_each": "${local.control_plane}"}],
	"talos_worker": [{"for_each": "${local.workers}"}],
	"talos_template": [{"for_each": "${toset(local.all_vm_hypervisors)}"}],
	"one_off": [{"name": "x"}],
}}}}

membership(collection, vm) := [{"for_each": collection, "pool_id": "${proxmox_virtual_environment_pool.p.pool_id}", "vm_id": sprintf("${proxmox_virtual_environment_vm.%s[each.key].vm_id}", [vm])}]

placing(m) := {"path": "modules/c/pools.tf", "contents": {"resource": {
	"proxmox_virtual_environment_pool": {"p": [{"pool_id": "${local.site_name}-workers"}]},
	"proxmox_pool_membership": m,
}}}

good := {
	"control_plane": membership("${local.control_plane}", "talos_cp"),
	"workers": membership("${local.workers}", "talos_worker"),
	"templates": membership("${toset(local.all_vm_hypervisors)}", "talos_template"),
}

test_memberships_named_for_their_function_that_read_the_machine_pass if {
	count(pools.deny) == 0 with input as [compute, placing(good)]
}

denies(m, words) if {
	some msg in pools.deny with input as [compute, placing(m)]
	contains(msg, words)
}

test_one_membership_over_a_map_is_refused if {
	denies({"by_function": membership("${local.everything}", "talos_cp")}, "there are 1 proxmox_pool_membership")
	denies({"by_function": membership("${local.everything}", "talos_cp")}, "named for the grouping rather than for a function")
}

test_a_vm_id_read_from_a_local_is_refused if {
	m := object.union(good, {"workers": [{"for_each": "${local.workers}", "vm_id": "${each.value.vm_id}"}]})
	denies(m, "no dependency edge")
}

test_a_membership_with_no_vm_id_is_refused if {
	denies(object.union(good, {"workers": [{"for_each": "${local.workers}"}]}), `"workers" sets no vm_id`)
}
