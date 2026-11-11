package rules.supplier_actions_test

import data.rules.supplier_actions
import rego.v1

sha := "0123456789012345678901234567890123456789"

list(actions) := {"path": "scripts/approved-suppliers.yml", "contents": {"actions": actions}}

workflow(refs) := {"path": ".github/workflows/w.yml", "contents": {"jobs": {"a": {"steps": [{"uses": r} | some r in refs]}}}}

used := workflow([$"actions/fetch@{sha}", $"someone/scans@{sha}", "./.github/workflows/local"])

listed := [
	{"action": "actions/fetch", "reason": "fetches"},
	{"action": "someone/scans", "reason": "scans"},
	{"action": "someone/installs", "called_by": ["someone/scans"], "reason": "run by the scan"},
]

says(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_every_action_listed_and_used_passes if {
	count(supplier_actions.deny) == 0 with input as [list(listed), used]
}

test_an_action_the_list_does_not_name_is_refused if {
	says(supplier_actions.deny, "a workflow uses another/thing, which is not in the approved suppliers list") with input as [list(listed), workflow([$"actions/fetch@{sha}", $"someone/scans@{sha}", $"another/thing@{sha}"])]
}

test_an_entry_nothing_uses_is_refused if {
	says(supplier_actions.deny, "names old/thing, which no workflow uses") with input as [list(array.concat(listed, [{"action": "old/thing", "reason": "once"}])), used]
}

test_a_caller_the_list_does_not_name_is_refused if {
	says(supplier_actions.deny, "says ghost/action runs someone/installs, but does not name ghost/action itself") with input as [list(array.concat(listed, [{"action": "someone/installs", "called_by": ["ghost/action"], "reason": "x"}])), used]
}

test_an_entry_whose_callers_are_all_unused_is_refused if {
	says(supplier_actions.deny, "names someone/installs for someone/scans, and no workflow uses any of them") with input as [list(listed), workflow([$"actions/fetch@{sha}"])]
}

test_no_actions_listed_is_refused if {
	says(supplier_actions.deny, "names no action") with input as [list([]), used]
}
