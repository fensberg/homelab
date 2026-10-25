package rules.plan_pool_ids_test

import data.rules.plan_pool_ids
import rego.v1

configured(resources) := {"path": "site-cluster.plan.json", "contents": {"configuration": {"root_module": {"module_calls": {"cluster": {"module": {"resources": resources}}}}}}}

pool(name, pool_id) := {"address": $"proxmox_virtual_environment_pool.{name}", "mode": "managed", "type": "proxmox_virtual_environment_pool", "expressions": {"pool_id": pool_id}}

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_a_pool_named_for_the_site_passes if {
	count(plan_pool_ids.deny) == 0 with input as [configured([pool("workers", {"references": ["local.site_name"]})])]
}

test_a_bare_pool_id_is_refused if {
	denies(plan_pool_ids.deny, "module.cluster.proxmox_virtual_environment_pool.workers has a pool id that does not include the site") with input as [configured([pool("workers", {"constant_value": "workers"})])]
}

test_a_pool_named_from_something_else_is_refused if {
	denies(plan_pool_ids.deny, "does not include the site") with input as [configured([pool("workers", {"references": ["local.other"]})])]
}

test_no_pool_at_all_is_refused if {
	denies(plan_pool_ids.deny, "checked nothing") with input as [configured([])]
}
