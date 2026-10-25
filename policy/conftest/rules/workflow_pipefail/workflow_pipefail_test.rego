package rules.workflow_pipefail_test

import data.rules.workflow_pipefail
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
	count(workflow_pipefail.deny) == 0 with input as [wf(object.union(base, {"true": {"push": {}}}))]
}

test_a_workflow_without_a_pipefail_shell_is_refused if denies(workflow_pipefail.deny, "does not set a pipefail shell") with input as [wf(object.remove(base, ["defaults"]))]
