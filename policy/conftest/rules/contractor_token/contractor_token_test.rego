package rules.contractor_token_test

import data.rules.contractor_token
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
	count(contractor_token.deny) == 0 with input as [wf(object.union(base, {"true": {"push": {}}}))]
}

test_a_contractor_step_with_no_token_is_refused if {
	denies(contractor_token.deny, "hands it no GitHub token") with input as [wf(object.union(base, {"jobs": object.union(base.jobs, {"converge": {"steps": [{"name": "Converge", "run": "./toolshed/contractor converge-site -site x"}]}})}))]
}

test_sterilizing_needs_no_token if {
	count(contractor_token.deny) == 0 with input as [wf(object.union(base, {"jobs": object.union(base.jobs, {"clean": {"steps": [{"run": "./toolshed/contractor build-site -phase sterilize"}]}})}))]
}

test_a_token_from_the_job_counts if {
	count(contractor_token.deny) == 0 with input as [wf(object.union(base, {"jobs": object.union(base.jobs, {"converge": {"env": {"GITHUB_TOKEN": "t"}, "steps": [{"run": "./toolshed/contractor converge-site"}]}})}))]
}

test_no_contractor_step_is_refused if {
	some msg in contractor_token.deny with input as [wf(object.remove(base, ["jobs"]))]
	contains(msg, "found no step that runs the contractor")
}
