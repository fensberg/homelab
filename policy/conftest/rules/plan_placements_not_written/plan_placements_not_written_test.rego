package rules.plan_placements_not_written_test

import data.rules.plan_placements_not_written
import rego.v1

configured(resources) := {"path": "site-cluster.plan.json", "contents": {"configuration": {"root_module": {"module_calls": {"cluster": {"module": {"resources": resources}}}}}}}

vm(expressions) := {"address": "proxmox_virtual_environment_vm.m", "mode": "managed", "type": "proxmox_virtual_environment_vm", "expressions": expressions}

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_placements_read_from_the_declarations_pass if {
	count(plan_placements_not_written.deny) == 0 with input as [configured([vm({"disk": [{"datastore_id": {"references": ["local.datastores"]}, "size": {"constant_value": 64}}], "network_device": [{"bridge": {"references": ["local.net.vnet"]}}]})])]
}

test_a_datastore_written_out_is_refused if {
	denies(plan_placements_not_written.deny, "module.cluster.proxmox_virtual_environment_vm.m writes its datastore_id as a string") with input as [configured([vm({"disk": [{"datastore_id": {"constant_value": "tank"}}]})])]
}

test_a_bridge_written_out_is_refused if {
	denies(plan_placements_not_written.deny, "writes its bridge as a string") with input as [configured([vm({"network_device": [{"bridge": {"constant_value": "vmbr0"}}]})])]
}

test_nothing_placed_is_refused if {
	denies(plan_placements_not_written.deny, "checked nothing") with input as [configured([vm({"name": {"constant_value": "x"}})])]
}
