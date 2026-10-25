package rules.app_token_inputs_test

import data.rules.app_token_inputs
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
	count(app_token_inputs.deny) == 0 with input as [wf(object.union(base, {"true": {"push": {}}}))]
}

test_a_client_id_fed_an_app_id_is_refused if {
	denies(app_token_inputs.deny, "Integration not found") with input as [wf(object.union(base, {"jobs": object.union(base.jobs, {"mint": {"steps": [{"with": {"client-id": "${{ secrets.A_BOT_APP_ID }}"}}]}})}))]
}
