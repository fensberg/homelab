package rules.plan_zone_off_switch_test

import data.rules.plan_zone_off_switch
import rego.v1

configured(resources) := {"path": "site-cluster.plan.json", "contents": {"configuration": {"root_module": {"module_calls": {"cluster": {"module": {"resources": resources}}}}}}}

resource(address, refs) := {"address": address, "mode": "managed", "type": "t", "expressions": {}, "for_each_expression": {"references": refs}}

denies(i, words) if {
	some msg in plan_zone_off_switch.deny with input as i
	contains(msg, words)
}

test_zone_resources_keyed_off_the_zone_pass if {
	count(plan_zone_off_switch.deny) == 0 with input as [configured([resource("t.dmz_image", ["local.dmz_hypervisors"]), resource("t.dmz", ["local.dmz"]), resource("t.workers", ["local.all_vm_hypervisors"])])]
}

test_a_zone_resource_keyed_off_every_hypervisor_is_refused if {
	denies([configured([resource("t.dmz_image", ["local.all_vm_hypervisors"])])], "module.cluster.t.dmz_image is a zone resource keyed off")
}

test_a_zone_data_source_is_held_too if {
	denies([configured([{"address": "data.t.dmz", "mode": "data", "type": "t", "expressions": {}, "for_each_expression": {"references": ["local.workers"]}}])], "data.t.dmz is a zone resource keyed off")
}

test_no_zone_resource_is_refused if {
	denies([configured([resource("t.workers", ["local.workers"])])], "checked nothing")
}
