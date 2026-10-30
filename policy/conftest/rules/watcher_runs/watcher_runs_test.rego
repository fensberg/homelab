package rules.watcher_runs_test

import data.rules.watcher_runs
import rego.v1

checkout := "actions/checkout@0123456789abcdef0123456789abcdef01234567"

clerk := {"path": ".github/workflows/clerk.yml", "contents": {"true": {"issue_comment": {}}, "jobs": {"read": {"steps": [{"uses": checkout, "with": {"persist-credentials": false}}, {"name": "Fetch", "run": "git fetch origin refs/pull/1/head\ngit worktree add --detach ../pr FETCH_HEAD"}]}}}}

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_a_clerk_that_reads_a_pull_request_as_data_passes if {
	count(watcher_runs.deny) == 0 with input as [clerk]
}

watcher(extra) := {"path": ".github/workflows/w.yml", "contents": object.union({"true": {"workflow_run": {}}, "permissions": {"issues": "write", "contents": "read"}, "jobs": {"j": {"steps": [{"uses": "$/.github/workflows/mobilize", "with": {"checkout": "false"}}, {"run": "echo ${{ github.event.workflow_run.conclusion }}"}]}}}, extra)}

test_a_watcher_reading_only_the_outcome_passes if {
	count(watcher_runs.deny) == 0 with input as [clerk, watcher({})]
}

test_a_watcher_reading_the_head_branch_is_refused if {
	w := watcher({"jobs": {"j": {"steps": [{"uses": "$/.github/workflows/mobilize", "with": {"checkout": "false"}}, {"run": "echo ${{ github.event.workflow_run.head_branch }}"}]}}})
	denies(watcher_runs.deny, "reads the watched run's head_branch") with input as [clerk, w]
}

test_a_watcher_that_checks_out_is_refused if {
	w := watcher({"jobs": {"j": {"steps": [{"uses": "$/.github/workflows/mobilize", "with": {"checkout": "false"}}, {"uses": checkout}]}}})
	denies(watcher_runs.deny, "checks out or downloads an artifact") with input as [clerk, w]
}

test_a_watcher_that_does_not_say_check_nothing_out_is_refused if {
	w := watcher({"jobs": {"j": {"steps": [{"uses": "$/.github/workflows/mobilize"}]}}})
	denies(watcher_runs.deny, "does not tell its first step to check nothing out") with input as [clerk, w]
}

test_a_watcher_that_may_write_is_refused if {
	denies(watcher_runs.deny, "may write contents") with input as [clerk, watcher({"permissions": {"contents": "write"}})]
}

test_a_watcher_that_does_not_spell_out_its_permissions_is_refused if {
	w := {"path": ".github/workflows/w.yml", "contents": {"true": {"workflow_run": {}}, "jobs": {}}}
	denies(watcher_runs.deny, "does not spell out its permissions") with input as [w]
}

test_a_watchers_job_is_held_to_the_same_permissions if {
	jobs := {"jobs": {"read": {"permissions": "write-all", "steps": []}, "write": {"permissions": {"contents": "write"}, "steps": []}}}
	denies(watcher_runs.deny, "in its job read, does not spell out its permissions") with input as [watcher(jobs)]
	denies(watcher_runs.deny, "in its job write, may write contents") with input as [watcher(jobs)]
}
