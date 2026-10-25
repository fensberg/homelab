package rules.plan_pool_names_test

import data.rules.plan_pool_names
import rego.v1

configured(resources) := {"path": "site-cluster.plan.json", "contents": {"configuration": {"root_module": {"module_calls": {"cluster": {"module": {"resources": resources}}}}}}}

member(name) := {"address": $"proxmox_pool_membership.{name}", "name": name, "mode": "managed", "type": "proxmox_pool_membership", "expressions": {}}

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_a_membership_per_function_passes if {
	count(plan_pool_names.deny) == 0 with input as [configured([member("workers"), member("control_plane")])]
}

test_one_membership_over_every_function_is_refused if {
	denies(plan_pool_names.deny, "there are 1 proxmox_pool_membership resources") with input as [configured([member("workers")])]
}

test_a_membership_named_for_the_grouping_is_refused if {
	denies(plan_pool_names.deny, "module.cluster.proxmox_pool_membership.by_function is named for the grouping") with input as [configured([member("workers"), member("by_function")])]
}
