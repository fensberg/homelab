package rules.plan_free_plan_test

import data.rules.plan_free_plan
import rego.v1

worker(address) := {"address": address, "mode": "managed", "type": "cloudflare_workers_script", "expressions": {}}

estate(resources, uses) := {"path": "/tmp/plans/estate/whole.plan.json", "contents": {"configuration": {"root_module": {
	"resources": resources,
	"outputs": {"free_plan_use": {"expression": {"references": ["module.free_plan.use", "module.free_plan"]}}},
	"module_calls": {
		"free_plan": {"expressions": {"uses": {"references": uses}}},
		"watchman": {"module": {"resources": [worker("cloudflare_workers_script.this")]}},
	},
}}}}

counted := estate([], ["module.watchman.use", "module.watchman"])

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_every_worker_counted_passes if {
	count(plan_free_plan.deny) == 0 with input as [counted]
}

test_a_worker_whose_module_is_not_counted_is_refused if {
	denies(plan_free_plan.deny, "module.watchman's use is not among what the free_plan_use output adds up") with input as [estate([], [])]
}

test_a_worker_in_the_root_is_refused if {
	denies(plan_free_plan.deny, "cloudflare_workers_script.loose is a Worker declared in the estate's root") with input as [estate([worker("cloudflare_workers_script.loose")], ["module.watchman.use"])]
}

test_nothing_to_read_is_refused if {
	denies(plan_free_plan.deny, "no Worker is in the estate's plan") with input as []
	denies(plan_free_plan.deny, "free_plan_use output reads no module") with input as []
}
