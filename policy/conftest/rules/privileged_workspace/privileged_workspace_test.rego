package rules.privileged_workspace_test

import data.rules.privileged_workspace
import rego.v1

checkout := "actions/checkout@0123456789abcdef0123456789abcdef01234567"

clerk := {"path": ".github/workflows/clerk.yml", "contents": {"true": {"issue_comment": {}}, "jobs": {"read": {"steps": [{"uses": checkout, "with": {"persist-credentials": false}}, {"name": "Fetch", "run": "git fetch origin refs/pull/1/head\ngit worktree add --detach ../pr FETCH_HEAD"}]}}}}

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_a_clerk_that_reads_a_pull_request_as_data_passes if {
	count(privileged_workspace.deny) == 0 with input as [clerk]
}

test_a_privileged_run_that_resets_the_workspace_is_refused if {
	bad := {"path": ".github/workflows/x.yml", "contents": {"true": {"pull_request_target": {}}, "jobs": {"j": {"steps": [{"name": "Reset", "run": "git reset --hard FETCH_HEAD"}]}}}}
	denies(privileged_workspace.deny, "runs \"git reset") with input as [clerk, bad]
}

test_a_privileged_fetch_without_a_worktree_is_refused if {
	bad := {"path": ".github/workflows/x.yml", "contents": {"true": {"workflow_run": {}}, "permissions": {"contents": "read"}, "jobs": {"j": {"steps": [{"name": "Fetch", "with": {"checkout": "false"}, "run": "git fetch origin refs/pull/1/head"}]}}}}
	denies(privileged_workspace.deny, "without its own worktree") with input as [clerk, bad]
}

watcher(extra) := {"path": ".github/workflows/w.yml", "contents": object.union({"true": {"workflow_run": {}}, "permissions": {"issues": "write", "contents": "read"}, "jobs": {"j": {"steps": [{"uses": "$/.github/workflows/mobilize", "with": {"checkout": "false"}}, {"run": "echo ${{ github.event.workflow_run.conclusion }}"}]}}}, extra)}

test_no_privileged_workflow_is_refused if {
	some msg in privileged_workspace.deny with input as []
	contains(msg, "no workflow runs on a privileged trigger")
}
