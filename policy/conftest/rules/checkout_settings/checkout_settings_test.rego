package rules.checkout_settings_test

import data.rules.checkout_settings
import rego.v1

checkout := "actions/checkout@0123456789abcdef0123456789abcdef01234567"

clerk := {"path": ".github/workflows/clerk.yml", "contents": {"true": {"issue_comment": {}}, "jobs": {"read": {"steps": [{"uses": checkout, "with": {"persist-credentials": false}}, {"name": "Fetch", "run": "git fetch origin refs/pull/1/head\ngit worktree add --detach ../pr FETCH_HEAD"}]}}}}

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_a_clerk_that_reads_a_pull_request_as_data_passes if {
	count(checkout_settings.deny) == 0 with input as [clerk]
}

test_a_checkout_pointed_at_other_code_is_refused if {
	other := {"path": ".github/workflows/checkout/action.yml", "contents": {"runs": {"using": "composite", "steps": [{"uses": checkout, "with": {"repository": "someone/else"}}]}}}
	denies(checkout_settings.deny, "gives actions/checkout \"repository\"") with input as [clerk, other]
}

watcher(extra) := {"path": ".github/workflows/w.yml", "contents": object.union({"true": {"workflow_run": {}}, "permissions": {"issues": "write", "contents": "read"}, "jobs": {"j": {"steps": [{"uses": "$/.github/workflows/mobilize", "with": {"checkout": "false"}}, {"run": "echo ${{ github.event.workflow_run.conclusion }}"}]}}}, extra)}

test_no_checkout_is_refused if {
	some msg in checkout_settings.deny with input as []
	contains(msg, "no actions/checkout step was found")
}
