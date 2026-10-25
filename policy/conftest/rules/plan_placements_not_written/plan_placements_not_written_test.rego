package rules.plan_placements_not_written_test

import data.rules.plan_placements_not_written
import rego.v1

configured(resources) := {"path": "site-cluster.plan.json", "contents": {"configuration": {"root_module": {"module_calls": {"cluster": {"module": {"resources": resources}}}}}}}

vm(expressions) := {"address": "proxmox_virtual_environment_vm.m", "mode": "managed", "type": "proxmox_virtual_environment_vm", "expressions": expressions}

denies(i, words) if {
	some msg in plan_placements_not_written.deny with input as i
	contains(msg, words)
}

test_placements_read_from_the_declarations_pass if {
	count(plan_placements_not_written.deny) == 0 with input as [configured([vm({"disk": [{"datastore_id": {"references": ["local.datastores"]}, "size": {"constant_value": 64}}], "network_device": [{"bridge": {"references": ["local.net.vnet"]}}]})])]
}

test_a_datastore_written_out_is_refused if {
	denies([configured([vm({"disk": [{"datastore_id": {"constant_value": "tank"}}]})])], "module.cluster.proxmox_virtual_environment_vm.m writes its datastore_id as a string")
}

test_a_bridge_written_out_is_refused if {
	denies([configured([vm({"network_device": [{"bridge": {"constant_value": "vmbr0"}}]})])], "writes its bridge as a string")
}

test_nothing_placed_is_refused if {
	denies([configured([vm({"name": {"constant_value": "x"}})])], "checked nothing")
}
