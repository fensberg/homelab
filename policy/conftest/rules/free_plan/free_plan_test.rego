package rules.free_plan_test

import data.rules.free_plan
import rego.v1

worker(name) := {"path": "management/estate/w/w.tf", "contents": {"resource": {"cloudflare_workers_script": {name: [{}]}}}}

counting(names) := {"path": "management/estate/free-plan.tf", "contents": {"locals": [{"workers_use": {n: "${module.x.use}" | some n in names}}]}}

test_a_counted_worker_passes if {
	count(free_plan.deny) == 0 with input as [worker("watchman"), counting(["watchman"])]
}

test_an_uncounted_worker_is_refused if {
	some msg in free_plan.deny with input as [worker("watchman"), counting(["watchmen"])]
	contains(msg, `the Worker "watchman", and it is not counted against the free plan`)
}

test_no_worker_is_refused if {
	some msg in free_plan.deny with input as [counting(["watchman"])]
	contains(msg, "found no Worker declared anywhere")
}
