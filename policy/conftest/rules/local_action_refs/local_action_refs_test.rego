package rules.local_action_refs_test

import data.rules.local_action_refs
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
	count(local_action_refs.deny) == 0 with input as [wf(object.union(base, {"true": {"push": {}}}))]
}

test_a_local_action_by_a_relative_path_is_refused if {
	some msg in local_action_refs.deny with input as [{"path": ".github/workflows/w.yml", "contents": {"defaults": {"run": {"shell": "bash"}}, "jobs": {"j": {"steps": [{"uses": "./.github/actions/versions"}]}}}}]
	contains(msg, "reaches a local action with a relative `uses: ./.github/actions/versions`")
}

any_relative if {
	some msg in local_action_refs.deny
	contains(msg, "reaches a local action")
}

test_a_local_action_by_the_repository_passes if {
	not any_relative with input as [{"path": ".github/workflows/w.yml", "contents": {"defaults": {"run": {"shell": "bash"}}, "jobs": {"j": {"steps": [{"uses": "$/.github/actions/versions"}]}}}}]
}
