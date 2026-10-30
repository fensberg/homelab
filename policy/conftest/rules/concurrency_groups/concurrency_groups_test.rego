package rules.concurrency_groups_test

import data.rules.concurrency_groups
import rego.v1

base := {"defaults": {"run": {"shell": "bash"}}, "concurrency": {"group": "g-${{ github.ref }}"}, "jobs": {
	"mint": {"steps": [{"with": {"client-id": "${{ secrets.A_BOT_CLIENT_ID }}"}}]},
	"converge": {"steps": [{"name": "Converge", "env": {"GH_TOKEN": "${{ github.token }}"}, "run": "./toolshed/contractor converge-site -site x"}]},
}}

wf(doc) := {"path": ".github/workflows/w.yml", "contents": doc}

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_a_workflow_in_shape_passes if {
	count(concurrency_groups.deny) == 0 with input as [wf(object.union(base, {"true": {"push": {}}}))]
}

group(on, c) := object.union(base, {"true": on, "concurrency": c})

test_a_push_only_workflow_may_key_by_anything if {
	count(concurrency_groups.deny) == 0 with input as [wf(group({"push": {"branches": ["main"]}}, {"group": "deploy-${{ github.ref }}", "cancel-in-progress": false}))]
}

test_a_pull_request_keyed_by_ref_is_refused if denies(concurrency_groups.deny, "github.event.pull_request.number") with input as [wf(group({"pull_request": {"types": ["closed"]}}, {"group": "plan-${{ github.ref }}"}))]

test_a_pull_request_and_a_push_sharing_a_key_is_refused if denies(concurrency_groups.deny, "github.event_name") with input as [wf(group({"pull_request": {}, "push": {}}, {"group": "d-${{ github.event.pull_request.number || github.ref }}"}))]

test_a_comment_keyed_by_the_pull_request_is_refused if denies(concurrency_groups.deny, "github.event.issue.number") with input as [wf(group({"pull_request": {}, "issue_comment": {}}, {"group": "c-${{ github.event.pull_request.number }}-${{ github.event_name }}"}))]

test_a_comment_that_cannot_tell_a_request_from_chatter_is_refused if denies(concurrency_groups.deny, "github.event.comment") with input as [wf(group({"issue_comment": {}}, {"group": "c-${{ github.event.issue.number }}"}))]

test_the_scalar_form_on_a_job_is_read if {
	doc := object.union(base, {"true": {"pull_request": {}}, "jobs": object.union(base.jobs, {"plan": {"concurrency": "p-${{ github.ref }}"}}), "concurrency": {"group": "${{ github.event.pull_request.number }}"}})
	denies(concurrency_groups.deny, "job plan") with input as [wf(doc)]
}

test_a_group_naming_everything_passes if {
	count(concurrency_groups.deny) == 0 with input as [wf(group({"pull_request": {}, "issue_comment": {}}, {"group": "c-${{ github.event.pull_request.number || github.event.issue.number }}-${{ github.event_name == 'issue_comment' && github.event.comment.id || 'read' }}"}))]
}

test_no_concurrency_group_is_refused if {
	some msg in concurrency_groups.deny with input as [wf(object.remove(base, ["concurrency"]))]
	contains(msg, "no concurrency group was found")
}
