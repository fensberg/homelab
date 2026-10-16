package rules.fabricator_describing_test

import data.rules.fabricator_describing
import rego.v1

wf(describe) := [{"path": ".github/workflows/fabricator.yml", "contents": {"jobs": {"package": {"steps": [
	{"name": "Number the version", "id": "number"},
	{"name": "Reuse a package of the same contents", "id": "reuse"},
	describe,
]}}}}]

step := {"name": "Describe the version", "env": {"VERSION": "${{ steps.number.outputs.version || steps.reuse.outputs.version }}"}}

denies(describe, words) if {
	some msg in fabricator_describing.deny with input as wf(describe)
	contains(msg, words)
}

test_a_step_handed_either_version_with_no_condition_passes if {
	count(fabricator_describing.deny) == 0 with input as wf(step)
}

test_a_condition_is_refused if {
	denies(object.union(step, {"if": "steps.number.outputs.version != ''"}), "runs only if")
}

test_a_version_from_one_step_only_is_refused if {
	denies(object.union(step, {"env": {"VERSION": "${{ steps.number.outputs.version }}"}}), `and not by the step "Reuse a package of the same contents"`)
}

test_no_describing_step_is_refused if {
	denies({"name": "Something else"}, "has no step that describes a version")
}
