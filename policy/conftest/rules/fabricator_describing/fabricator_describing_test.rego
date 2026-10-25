package rules.fabricator_describing_test

import data.rules.fabricator_describing
import rego.v1

wf(describe) := [{"path": ".github/workflows/fabricator.yml", "contents": {"jobs": {"package": {"steps": [
	{"name": "Number the version", "id": "number"},
	{"name": "Reuse a package of the same contents", "id": "reuse"},
	describe,
]}}}}]

step := {"name": "Describe the version", "env": {"VERSION": "${{ steps.number.outputs.version || steps.reuse.outputs.version }}"}}

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_a_step_handed_either_version_with_no_condition_passes if {
	count(fabricator_describing.deny) == 0 with input as wf(step)
}

test_a_condition_is_refused if {
	denies(fabricator_describing.deny, "runs only if") with input as wf(object.union(step, {"if": "steps.number.outputs.version != ''"}))
}

test_a_version_from_one_step_only_is_refused if {
	denies(fabricator_describing.deny, `and not by the step "Reuse a package of the same contents"`) with input as wf(object.union(step, {"env": {"VERSION": "${{ steps.number.outputs.version }}"}}))
}

test_no_describing_step_is_refused if {
	denies(fabricator_describing.deny, "has no step that describes a version") with input as wf({"name": "Something else"})
}
