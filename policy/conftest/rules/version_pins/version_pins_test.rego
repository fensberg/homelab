package rules.version_pins_test

import data.rules.version_pins
import rego.v1

env(pins) := {"path": "scripts/versions.env", "contents": pins}

exporter := {"path": ".github/actions/versions/action.yml", "contents": {"runs": {"steps": [{"run": "pins='^[A-Z][A-Z0-9_]*_VERSION=[A-Za-z0-9][A-Za-z0-9._+-]*$'"}]}}}

wf(contents) := {"path": ".github/workflows/w.yml", "contents": contents}

pins := env({"TOFU_VERSION": "1.12.6", "GO_VERSION": "1.26.7"})

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

# setup is a job's setup actions, each told its version as it is given.
setup(go, node) := {"setup": {"steps": [{"with": {"go-version": go}}, {"with": {"node-version": node}}]}}

reads := {"path": ".github/workflows/setup.yml", "contents": {"jobs": setup("${{ env.GO_VERSION }}", "${{ env.NODE_VERSION }}")}}

test_references_to_a_pin_pass if {
	w := wf({"env": {"TOFU_VERSION": "${{ env.TOFU_VERSION }}"}, "jobs": object.union(setup("${{ env.GO_VERSION }}", "${{ env.NODE_VERSION }}"), {"j": {"steps": [{"run": "docker build --build-arg TOFU_VERSION=\"$TOFU_VERSION\" ."}]}})})
	count(version_pins.deny) == 0 with input as [pins, exporter, w]
}

test_a_literal_in_yaml_is_refused if {
	denies(version_pins.deny, "declares TOFU_VERSION itself") with input as [pins, exporter, reads, wf({"env": {"TOFU_VERSION": "1.10.6"}})]
}

test_a_literal_number_in_yaml_is_refused if {
	denies(version_pins.deny, "declares GO_VERSION itself") with input as [pins, exporter, reads, wf({"jobs": {"j": {"env": {"GO_VERSION": 1.26}}}})]
}

test_a_literal_in_a_script_is_refused if {
	denies(version_pins.deny, "declares GO_VERSION itself") with input as [pins, exporter, reads, wf({"jobs": {"j": {"steps": [{"run": "set -e\nGO_VERSION=1.25\ngo version"}]}}})]
}

test_no_pins_is_refused if {
	denies(version_pins.deny, "declares nothing") with input as [env({}), exporter, reads]
}

test_a_restated_setup_version_is_refused if {
	denies(version_pins.deny, "restates a pinned version: go-version: 1.26.7") with input as [pins, wf({"jobs": setup("1.26.7", "${{ env.NODE_VERSION }}")})]
}

test_too_few_setup_inputs_is_refused if {
	denies(version_pins.deny, "found only 1 version input(s)") with input as [pins, wf({"jobs": {"j": {"steps": [{"with": {"go-version": "${{ env.GO_VERSION }}"}}]}}})]
}
