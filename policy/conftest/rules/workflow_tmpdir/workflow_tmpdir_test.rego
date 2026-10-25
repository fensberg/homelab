package rules.workflow_tmpdir_test

import data.rules.workflow_tmpdir
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
	count(workflow_tmpdir.deny) == 0 with input as [wf(object.union(base, {"true": {"push": {}}}))]
}

test_a_step_reading_tmpdir_is_refused if {
	denies(workflow_tmpdir.deny, "reads $TMPDIR") with input as [wf(object.union(base, {"jobs": object.union(base.jobs, {"t": {"steps": [{"run": "cd \"${TMPDIR}\""}]}})}))]
}

test_a_comment_in_a_script_naming_tmpdir_is_not_a_read if {
	count(workflow_tmpdir.deny) == 0 with input as [wf(object.union(base, {"jobs": object.union(base.jobs, {"t": {"steps": [{"run": "# never $TMPDIR\ncd \"$RUNNER_TEMP\""}]}})}))]
}

test_no_workflow_is_refused if {
	some msg in workflow_tmpdir.deny with input as []
	contains(msg, "found no workflows")
}
