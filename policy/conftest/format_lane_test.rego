package format_lane_test

import data.format_lane
import rego.v1

setup_go := {"path": ".github/workflows/setup-go/action.yml", "contents": {"runs": {"using": "composite", "steps": [{"uses": "actions/setup-go@0123", "with": {"go-version": "${{ env.GO_VERSION }}"}}]}}}

lane(steps) := {"path": ".github/workflows/pr-validation.yml", "contents": {"jobs": {"format": {"steps": steps}}}}

go := {"uses": "$/.github/workflows/setup-go"}

gate := {"run": "go run -C scripts/security . guard-deliveries -before-install"}

deliver := {"run": "scripts/take-delivery.sh pre-commit pre-commit-hooks"}

run := {"run": "pre-commit run --all-files --show-diff-on-failure"}

denies(i, words) if {
	some msg in format_lane.deny with input as i
	contains(msg, words)
}

test_the_gate_first_on_the_pinned_go_passes if {
	count(format_lane.deny) == 0 with input as [setup_go, lane([go, gate, deliver, run])]
}

test_no_gate_is_refused if {
	denies([setup_go, lane([go, deliver, run])], "does not run the supplier gate before installing")
}

test_a_delivery_before_the_gate_is_refused if {
	denies([setup_go, lane([go, deliver, gate, run])], `runs "scripts/take-delivery.sh" before the supplier gate`)
}

test_go_on_a_version_of_its_own_is_refused if {
	own := {"path": ".github/workflows/setup-go/action.yml", "contents": {"runs": {"steps": [{"with": {"go-version": "1.26.7"}}]}}}
	denies([own, lane([go, gate, deliver, run])], "sets up Go without reading GO_VERSION")
}

test_a_shared_action_that_takes_a_version_is_refused if {
	restatable := {"path": ".github/workflows/setup-go/action.yml", "contents": {"inputs": {"go-version": {}}, "runs": {"steps": [{"with": {"go-version": "${{ env.GO_VERSION }}"}}]}}}
	denies([restatable, lane([go, gate, deliver, run])], "sets up Go without reading GO_VERSION")
}

test_no_format_lane_is_refused if {
	denies([setup_go], "no job runs pre-commit run --all-files")
}
