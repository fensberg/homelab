package rules.dispatch_inputs_test

import data.rules.dispatch_inputs
import rego.v1

checkout := "actions/checkout@0123456789abcdef0123456789abcdef01234567"

clerk := {"path": ".github/workflows/clerk.yml", "contents": {"true": {"issue_comment": {}}, "jobs": {"read": {"steps": [{"uses": checkout, "with": {"persist-credentials": false}}, {"name": "Fetch", "run": "git fetch origin refs/pull/1/head\ngit worktree add --detach ../pr FETCH_HEAD"}]}}}}

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_a_clerk_that_reads_a_pull_request_as_data_passes if {
	count(dispatch_inputs.deny) == 0 with input as [clerk]
}

test_a_dispatched_workflow_with_inputs_that_writes_is_refused if {
	bad := {"path": ".github/workflows/d.yml", "contents": {"true": {"workflow_dispatch": {"inputs": {"tier": {}}}}, "permissions": {"contents": "write"}, "jobs": {}}}
	denies(dispatch_inputs.deny, "may write contents") with input as [clerk, bad]
}

test_a_dispatched_workflow_with_inputs_and_no_permissions_is_refused if {
	bad := {"path": ".github/workflows/d.yml", "contents": {"true": {"workflow_dispatch": {"inputs": {"tier": {}}}}, "jobs": {}}}
	denies(dispatch_inputs.deny, "declares no permissions") with input as [clerk, bad]
}

test_a_dispatched_workflow_with_inputs_that_reads_passes if {
	ok := {"path": ".github/workflows/d.yml", "contents": {"true": {"workflow_dispatch": {"inputs": {"tier": {}}}}, "permissions": {"contents": "read"}, "jobs": {"t": {"permissions": {}}}}}
	count(dispatch_inputs.deny) == 0 with input as [clerk, ok]
}

watcher(extra) := {"path": ".github/workflows/w.yml", "contents": object.union({"true": {"workflow_run": {}}, "permissions": {"issues": "write", "contents": "read"}, "jobs": {"j": {"steps": [{"uses": "$/.github/workflows/mobilize", "with": {"checkout": "false"}}, {"run": "echo ${{ github.event.workflow_run.conclusion }}"}]}}}, extra)}

dispatched(jobs) := {"path": ".github/workflows/d.yml", "contents": {"on": {"workflow_dispatch": {"inputs": {"what": {}}}}, "permissions": "read-all", "jobs": jobs}}

test_a_dispatched_job_that_may_write_is_refused if {
	some msg in dispatch_inputs.deny with input as [dispatched({"publish": {"permissions": {"contents": "write"}}})]
	contains(msg, "its job publish may write contents")
}

test_a_dispatched_job_with_write_all_is_refused if {
	some msg in dispatch_inputs.deny with input as [dispatched({"publish": {"permissions": "write-all"}})]
	contains(msg, "has permissions: write-all")
}
