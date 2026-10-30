package rules.job_timeout_test

import data.rules.job_timeout
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
	count(job_timeout.deny) == 0 with input as [workflow({"j": job([harden, checkout({"persist-credentials": false})])})]
}

test_an_unbounded_job_is_refused if {
	denies(job_timeout.deny, "declares no timeout-minutes") with input as [workflow({"j": {"runs-on": "x", "steps": [harden]}})]
}

test_no_jobs_at_all_is_refused if {
	denies(job_timeout.deny, "found no jobs in any workflow") with input as []
}
