package workflow_trust_test

import data.workflow_trust
import rego.v1

checkout := "actions/checkout@0123456789abcdef0123456789abcdef01234567"

clerk := {"path": ".github/workflows/clerk.yml", "contents": {"true": {"issue_comment": {}}, "jobs": {"read": {"steps": [{"uses": checkout, "with": {"persist-credentials": false}}, {"name": "Fetch", "run": "git fetch origin refs/pull/1/head\ngit worktree add --detach ../pr FETCH_HEAD"}]}}}}

denies(files, words) if {
	some msg in workflow_trust.deny with input as files
	contains(msg, words)
}

test_a_clerk_that_reads_a_pull_request_as_data_passes if {
	count(workflow_trust.deny) == 0 with input as [clerk]
}

test_a_checkout_pointed_at_other_code_is_refused if {
	other := {"path": ".github/workflows/checkout/action.yml", "contents": {"runs": {"using": "composite", "steps": [{"uses": checkout, "with": {"repository": "someone/else"}}]}}}
	denies([clerk, other], "gives actions/checkout \"repository\"")
}

test_a_privileged_run_that_resets_the_workspace_is_refused if {
	bad := {"path": ".github/workflows/x.yml", "contents": {"true": {"pull_request_target": {}}, "jobs": {"j": {"steps": [{"name": "Reset", "run": "git reset --hard FETCH_HEAD"}]}}}}
	denies([clerk, bad], "runs \"git reset")
}

test_a_privileged_fetch_without_a_worktree_is_refused if {
	bad := {"path": ".github/workflows/x.yml", "contents": {"true": {"workflow_run": {}}, "permissions": {"contents": "read"}, "jobs": {"j": {"steps": [{"name": "Fetch", "with": {"checkout": "false"}, "run": "git fetch origin refs/pull/1/head"}]}}}}
	denies([clerk, bad], "without its own worktree")
}

test_a_dispatched_workflow_with_inputs_that_writes_is_refused if {
	bad := {"path": ".github/workflows/d.yml", "contents": {"true": {"workflow_dispatch": {"inputs": {"tier": {}}}}, "permissions": {"contents": "write"}, "jobs": {}}}
	denies([clerk, bad], "may write contents")
}

test_a_dispatched_workflow_with_inputs_and_no_permissions_is_refused if {
	bad := {"path": ".github/workflows/d.yml", "contents": {"true": {"workflow_dispatch": {"inputs": {"tier": {}}}}, "jobs": {}}}
	denies([clerk, bad], "declares no permissions")
}

test_a_dispatched_workflow_with_inputs_that_reads_passes if {
	ok := {"path": ".github/workflows/d.yml", "contents": {"true": {"workflow_dispatch": {"inputs": {"tier": {}}}}, "permissions": {"contents": "read"}, "jobs": {"t": {"permissions": {}}}}}
	count(workflow_trust.deny) == 0 with input as [clerk, ok]
}

watcher(extra) := {"path": ".github/workflows/w.yml", "contents": object.union({"true": {"workflow_run": {}}, "permissions": {"issues": "write", "contents": "read"}, "jobs": {"j": {"steps": [{"uses": "$/.github/workflows/mobilize", "with": {"checkout": "false"}}, {"run": "echo ${{ github.event.workflow_run.conclusion }}"}]}}}, extra)}

test_a_watcher_reading_only_the_outcome_passes if {
	count(workflow_trust.deny) == 0 with input as [clerk, watcher({})]
}

test_a_watcher_reading_the_head_branch_is_refused if {
	w := watcher({"jobs": {"j": {"steps": [{"uses": "$/.github/workflows/mobilize", "with": {"checkout": "false"}}, {"run": "echo ${{ github.event.workflow_run.head_branch }}"}]}}})
	denies([clerk, w], "reads the watched run's head_branch")
}

test_a_watcher_that_checks_out_is_refused if {
	w := watcher({"jobs": {"j": {"steps": [{"uses": "$/.github/workflows/mobilize", "with": {"checkout": "false"}}, {"uses": checkout}]}}})
	denies([clerk, w], "checks out or downloads an artifact")
}

test_a_watcher_that_does_not_say_check_nothing_out_is_refused if {
	w := watcher({"jobs": {"j": {"steps": [{"uses": "$/.github/workflows/mobilize"}]}}})
	denies([clerk, w], "does not tell its first step to check nothing out")
}

test_a_watcher_that_may_write_is_refused if {
	denies([clerk, watcher({"permissions": {"contents": "write"}})], "may write contents")
}
