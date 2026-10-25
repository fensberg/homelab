package rules.format_lane_go_test

import data.rules.format_lane_go
import rego.v1

setup_go := {"path": ".github/workflows/setup-go/action.yml", "contents": {"runs": {"using": "composite", "steps": [{"uses": "actions/setup-go@0123", "with": {"go-version": "${{ env.GO_VERSION }}"}}]}}}

lane(steps) := {"path": ".github/workflows/pr-validation.yml", "contents": {"jobs": {"format": {"steps": steps}}}}

go := {"uses": "$/.github/workflows/setup-go"}

gate := {"run": "go run -C scripts/security . guard-deliveries -before-install"}

deliver := {"run": "scripts/take-delivery.sh pre-commit pre-commit-hooks"}

run := {"run": "pre-commit run --all-files --show-diff-on-failure"}

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_the_gate_first_on_the_pinned_go_passes if {
	count(format_lane_go.deny) == 0 with input as [setup_go, lane([go, gate, deliver, run])]
}

test_go_on_a_version_of_its_own_is_refused if {
	own := {"path": ".github/workflows/setup-go/action.yml", "contents": {"runs": {"steps": [{"with": {"go-version": "1.26.7"}}]}}}
	denies(format_lane_go.deny, "sets up Go without reading GO_VERSION") with input as [own, lane([go, gate, deliver, run])]
}

test_a_shared_action_that_takes_a_version_is_refused if {
	restatable := {"path": ".github/workflows/setup-go/action.yml", "contents": {"inputs": {"go-version": {}}, "runs": {"steps": [{"with": {"go-version": "${{ env.GO_VERSION }}"}}]}}}
	denies(format_lane_go.deny, "sets up Go without reading GO_VERSION") with input as [restatable, lane([go, gate, deliver, run])]
}
