package rules.workflow_jobs_test

import data.rules.workflow_jobs
import rego.v1

harden := {"uses": "step-security/harden-runner@0123456789abcdef0123456789abcdef01234567"}

checkout(settings) := {"uses": "actions/checkout@0123456789abcdef0123456789abcdef01234567", "with": settings}

workflow(jobs) := {"path": ".github/workflows/w.yml", "contents": {"jobs": jobs}}

action(dir, steps) := {"path": $"{dir}/action.yml", "contents": {"runs": {"using": "composite", "steps": steps}}}

job(steps) := {"runs-on": "ubuntu-latest", "timeout-minutes": 5, "steps": steps}

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_a_hardened_bounded_job_with_a_safe_checkout_passes if {
	count(workflow_jobs.deny) == 0 with input as [workflow({"j": job([harden, checkout({"persist-credentials": false})])})]
}

test_a_job_not_hardened_first_is_refused if {
	denies(workflow_jobs.deny, "does not begin with harden-runner") with input as [workflow({"j": job([checkout({"persist-credentials": false}), harden])})]
}

test_a_job_may_begin_with_an_action_that_hardens_first if {
	files := [
		workflow({"j": job([{"uses": "$/.github/workflows/mobilize"}])}),
		action(".github/workflows/mobilize", [harden, checkout({"persist-credentials": false})]),
	]
	count(workflow_jobs.deny) == 0 with input as files
}

test_an_action_that_does_not_harden_first_is_refused if {
	files := [
		workflow({"j": job([{"uses": "$/.github/actions/versions"}])}),
		action(".github/actions/versions", [{"name": "Export pinned versions", "run": "true"}]),
	]
	denies(workflow_jobs.deny, "whose own first step is \"Export pinned versions\"") with input as files
}

test_an_action_that_cannot_be_read_is_refused if {
	denies(workflow_jobs.deny, "that action cannot be read") with input as [workflow({"j": job([{"uses": "$/.github/actions/gone"}])})]
}

test_a_job_with_no_steps_is_refused if {
	denies(workflow_jobs.deny, "has no steps") with input as [workflow({"j": {"runs-on": "x", "timeout-minutes": 1}})]
}

test_an_unbounded_job_is_refused if {
	denies(workflow_jobs.deny, "declares no timeout-minutes") with input as [workflow({"j": {"runs-on": "x", "steps": [harden]}})]
}

test_a_checkout_that_persists_credentials_is_refused if {
	denies(workflow_jobs.deny, "checks out without persist-credentials: false") with input as [workflow({"j": job([harden, checkout({})])})]
}

test_a_checkout_two_actions_deep_is_found if {
	files := [
		workflow({"j": job([{"uses": "$/.github/workflows/mobilize"}])}),
		action(".github/workflows/mobilize", [harden, {"uses": "$/.github/workflows/checkout"}]),
		action(".github/workflows/checkout", [checkout({"persist-credentials": true})]),
	]
	denies(workflow_jobs.deny, "(inside $/.github/workflows/mobilize) (inside $/.github/workflows/checkout)") with input as files
}

test_actions_reached_in_a_loop_are_refused if {
	files := [
		workflow({"j": job([harden, {"uses": "$/.github/actions/a"}])}),
		action(".github/actions/a", [{"uses": "$/.github/actions/b"}]),
		action(".github/actions/b", [{"uses": "$/.github/actions/a"}]),
	]
	denies(workflow_jobs.deny, "more than four deep") with input as files
}

test_no_jobs_at_all_is_refused if {
	denies(workflow_jobs.deny, "found no jobs in any workflow") with input as []
}
