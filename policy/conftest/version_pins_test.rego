package version_pins_test

import data.version_pins
import rego.v1

env(pins) := {"path": "scripts/versions.env", "contents": pins}

exporter := {"path": ".github/actions/versions/action.yml", "contents": {"runs": {"steps": [{"run": "pins='^[A-Z][A-Z0-9_]*_VERSION=[A-Za-z0-9][A-Za-z0-9._+-]*$'"}]}}}

wf(contents) := {"path": ".github/workflows/w.yml", "contents": contents}

pins := env({"TOFU_VERSION": "1.12.6", "GO_VERSION": "1.26.7"})

denies(i, words) if {
	some msg in version_pins.deny with input as i
	contains(msg, words)
}

test_references_to_a_pin_pass if {
	w := wf({"env": {"TOFU_VERSION": "${{ env.TOFU_VERSION }}"}, "jobs": {"j": {"steps": [{"run": "docker build --build-arg TOFU_VERSION=\"$TOFU_VERSION\" ."}]}}})
	count(version_pins.deny) == 0 with input as [pins, exporter, w]
}

test_a_literal_in_yaml_is_refused if {
	denies([pins, exporter, wf({"env": {"TOFU_VERSION": "1.10.6"}})], "declares TOFU_VERSION itself")
}

test_a_literal_number_in_yaml_is_refused if {
	denies([pins, exporter, wf({"jobs": {"j": {"env": {"GO_VERSION": 1.26}}}})], "declares GO_VERSION itself")
}

test_a_literal_in_a_script_is_refused if {
	denies([pins, exporter, wf({"jobs": {"j": {"steps": [{"run": "set -e\nGO_VERSION=1.25\ngo version"}]}}})], "declares GO_VERSION itself")
}

test_a_pin_the_action_will_not_export_is_refused if {
	denies([env({"TOFU_VERSION": "1.12.6", "TOFU_SHA256": "abc"}), exporter], `declares "TOFU_SHA256"`)
	denies([env({"TOFU_VERSION": "-rc1"}), exporter], `declares "TOFU_VERSION"`)
}

test_an_exporter_that_writes_anything_is_refused if {
	loose := {"path": ".github/actions/versions/action.yml", "contents": {"runs": {"steps": [{"run": "cat scripts/versions.env >> \"$GITHUB_ENV\""}]}}}
	denies([pins, loose], "no longer restricts what it writes")
}

test_no_exporter_is_refused if {
	denies([pins], "is missing")
}

test_no_pins_is_refused if {
	denies([env({}), exporter], "declares nothing")
}
