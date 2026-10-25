package rules.job_checkouts_test

import data.rules.job_checkouts
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
	count(job_checkouts.deny) == 0 with input as [workflow({"j": job([harden, checkout({"persist-credentials": false})])})]
}

test_a_checkout_that_persists_credentials_is_refused if {
	denies(job_checkouts.deny, "checks out without persist-credentials: false") with input as [workflow({"j": job([harden, checkout({})])})]
}

test_a_checkout_two_actions_deep_is_found if {
	files := [
		workflow({"j": job([{"uses": "$/.github/workflows/mobilize"}])}),
		action(".github/workflows/mobilize", [harden, {"uses": "$/.github/workflows/checkout"}]),
		action(".github/workflows/checkout", [checkout({"persist-credentials": true})]),
	]
	denies(job_checkouts.deny, "(inside $/.github/workflows/mobilize) (inside $/.github/workflows/checkout)") with input as files
}

test_actions_reached_in_a_loop_are_refused if {
	files := [
		workflow({"j": job([harden, {"uses": "$/.github/actions/a"}])}),
		action(".github/actions/a", [{"uses": "$/.github/actions/b"}]),
		action(".github/actions/b", [{"uses": "$/.github/actions/a"}]),
	]
	denies(job_checkouts.deny, "more than four deep") with input as files
}
