package rules.plan_pool_ids_test

import data.rules.plan_pool_ids
import rego.v1

configured(resources) := {"path": "site-cluster.plan.json", "contents": {"configuration": {"root_module": {"module_calls": {"cluster": {"module": {"resources": resources}}}}}}}

pool(name, pool_id) := {"address": sprintf("proxmox_virtual_environment_pool.%s", [name]), "mode": "managed", "type": "proxmox_virtual_environment_pool", "expressions": {"pool_id": pool_id}}

denies(i, words) if {
	some msg in plan_pool_ids.deny with input as i
	contains(msg, words)
}

test_a_pool_named_for_the_site_passes if {
	count(plan_pool_ids.deny) == 0 with input as [configured([pool("workers", {"references": ["local.site_name"]})])]
}

test_a_bare_pool_id_is_refused if {
	denies([configured([pool("workers", {"constant_value": "workers"})])], "module.cluster.proxmox_virtual_environment_pool.workers has a pool id that does not include the site")
}

test_a_pool_named_from_something_else_is_refused if {
	denies([configured([pool("workers", {"references": ["local.other"]})])], "does not include the site")
}

test_no_pool_at_all_is_refused if {
	denies([configured([])], "checked nothing")
}
