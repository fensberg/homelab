package rules.workflow_shape_test

import data.rules.workflow_shape
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
	count(workflow_shape.deny) == 0 with input as [wf(object.union(base, {"true": {"push": {}}}))]
}

test_a_workflow_without_a_pipefail_shell_is_refused if denies(workflow_shape.deny, "does not set a pipefail shell") with input as [wf(object.remove(base, ["defaults"]))]

test_a_step_reading_tmpdir_is_refused if {
	denies(workflow_shape.deny, "reads $TMPDIR") with input as [wf(object.union(base, {"jobs": object.union(base.jobs, {"t": {"steps": [{"run": "cd \"${TMPDIR}\""}]}})}))]
}

test_a_comment_in_a_script_naming_tmpdir_is_not_a_read if {
	count(workflow_shape.deny) == 0 with input as [wf(object.union(base, {"jobs": object.union(base.jobs, {"t": {"steps": [{"run": "# never $TMPDIR\ncd \"$RUNNER_TEMP\""}]}})}))]
}

test_a_client_id_fed_an_app_id_is_refused if {
	denies(workflow_shape.deny, "Integration not found") with input as [wf(object.union(base, {"jobs": object.union(base.jobs, {"mint": {"steps": [{"with": {"client-id": "${{ secrets.A_BOT_APP_ID }}"}}]}})}))]
}

test_a_contractor_step_with_no_token_is_refused if {
	denies(workflow_shape.deny, "hands it no GitHub token") with input as [wf(object.union(base, {"jobs": object.union(base.jobs, {"converge": {"steps": [{"name": "Converge", "run": "./toolshed/contractor converge-site -site x"}]}})}))]
}

test_sterilizing_needs_no_token if {
	count(workflow_shape.deny) == 0 with input as [wf(object.union(base, {"jobs": object.union(base.jobs, {"clean": {"steps": [{"run": "./toolshed/contractor build-site -phase sterilize"}]}})}))]
}

test_a_token_from_the_job_counts if {
	count(workflow_shape.deny) == 0 with input as [wf(object.union(base, {"jobs": object.union(base.jobs, {"converge": {"env": {"GITHUB_TOKEN": "t"}, "steps": [{"run": "./toolshed/contractor converge-site"}]}})}))]
}

group(on, c) := object.union(base, {"true": on, "concurrency": c})

test_a_push_only_workflow_may_key_by_anything if {
	count(workflow_shape.deny) == 0 with input as [wf(group({"push": {"branches": ["main"]}}, {"group": "deploy-${{ github.ref }}", "cancel-in-progress": false}))]
}

test_a_pull_request_keyed_by_ref_is_refused if denies(workflow_shape.deny, "github.event.pull_request.number") with input as [wf(group({"pull_request": {"types": ["closed"]}}, {"group": "plan-${{ github.ref }}"}))]

test_a_pull_request_and_a_push_sharing_a_key_is_refused if denies(workflow_shape.deny, "github.event_name") with input as [wf(group({"pull_request": {}, "push": {}}, {"group": "d-${{ github.event.pull_request.number || github.ref }}"}))]

test_a_comment_keyed_by_the_pull_request_is_refused if denies(workflow_shape.deny, "github.event.issue.number") with input as [wf(group({"pull_request": {}, "issue_comment": {}}, {"group": "c-${{ github.event.pull_request.number }}-${{ github.event_name }}"}))]

test_a_comment_that_cannot_tell_a_request_from_chatter_is_refused if denies(workflow_shape.deny, "github.event.comment") with input as [wf(group({"issue_comment": {}}, {"group": "c-${{ github.event.issue.number }}"}))]

test_the_scalar_form_on_a_job_is_read if {
	doc := object.union(base, {"true": {"pull_request": {}}, "jobs": object.union(base.jobs, {"plan": {"concurrency": "p-${{ github.ref }}"}}), "concurrency": {"group": "${{ github.event.pull_request.number }}"}})
	denies(workflow_shape.deny, "job plan") with input as [wf(doc)]
}

test_a_group_naming_everything_passes if {
	count(workflow_shape.deny) == 0 with input as [wf(group({"pull_request": {}, "issue_comment": {}}, {"group": "c-${{ github.event.pull_request.number || github.event.issue.number }}-${{ github.event_name == 'issue_comment' && github.event.comment.id || 'read' }}"}))]
}

test_a_local_action_by_a_relative_path_is_refused if {
	some msg in workflow_shape.deny with input as [{"path": ".github/workflows/w.yml", "contents": {"defaults": {"run": {"shell": "bash"}}, "jobs": {"j": {"steps": [{"uses": "./.github/actions/versions"}]}}}}]
	contains(msg, "reaches a local action with a relative `uses: ./.github/actions/versions`")
}

test_a_local_action_by_the_repository_passes if {
	not any_relative with input as [{"path": ".github/workflows/w.yml", "contents": {"defaults": {"run": {"shell": "bash"}}, "jobs": {"j": {"steps": [{"uses": "$/.github/actions/versions"}]}}}}]
}

any_relative if {
	some msg in workflow_shape.deny
	contains(msg, "reaches a local action")
}
