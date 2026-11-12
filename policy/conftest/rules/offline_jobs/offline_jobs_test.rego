package rules.offline_jobs_test

import data.rules.offline_jobs
import rego.v1

list(policy) := {"path": "scripts/approved-suppliers.yml", "contents": {"egress": [
	{"job": "w.yml/check", "policy": policy, "hosts": ["proxy.golang.org:443"]},
	{"job": "w.yml/other", "policy": "block", "hosts": []},
]}}

workflow(steps) := {"path": ".github/workflows/w.yml", "contents": {"jobs": {
	"check": {"steps": steps},
	"other": {"steps": [{"run": "make"}]},
}}}

mobilize := {"uses": "$/.github/workflows/mobilize"}

offline := {"name": "The check", "uses": "$/.github/workflows/offline", "with": {"run": "go test ./..."}}

says(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_an_offline_job_of_actions_alone_passes if {
	count(offline_jobs.deny) == 0 with input as [list("offline"), workflow([mobilize, offline])]
}

test_a_step_of_its_own_is_refused if {
	says(offline_jobs.deny, "w.yml/check is offline, and its step 2 (Setup) runs on the runner") with input as [list("offline"), workflow([mobilize, {"name": "Setup", "run": "go mod download"}, offline])]
}

test_an_unnamed_step_is_named_by_its_place if {
	says(offline_jobs.deny, "its step 1 (unnamed)") with input as [list("offline"), workflow([{"run": "true"}, offline])]
}

test_no_offline_step_is_refused if {
	says(offline_jobs.deny, "no step of it runs $/.github/workflows/offline") with input as [list("offline"), workflow([mobilize])]
}

test_a_declared_job_no_workflow_has_is_refused if {
	suppliers := {"path": "scripts/approved-suppliers.yml", "contents": {"egress": [{"job": "w.yml/gone", "policy": "offline"}]}}
	says(offline_jobs.deny, "declares w.yml/gone offline, and no workflow has that job") with input as [suppliers, workflow([mobilize, offline])]
}

test_no_offline_job_is_refused if {
	says(offline_jobs.deny, "no job in the egress section is offline") with input as [list("block"), workflow([mobilize, offline])]
}
