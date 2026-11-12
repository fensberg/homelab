package rules.supplier_actions_test

import data.rules.supplier_actions
import rego.v1

sha := "0123456789012345678901234567890123456789"

list(actions) := {"path": "scripts/approved-suppliers.yml", "contents": {"actions": actions}}

workflow(refs) := {"path": ".github/workflows/w.yml", "contents": {"jobs": {"a": {"steps": [{"uses": r} | some r in refs]}}}}

used := workflow([$"actions/fetch@{sha}", $"someone/scans@{sha}", "$/.github/workflows/shared"])

listed := [
	{"action": "actions/fetch", "reason": "fetches"},
	{"action": "someone/scans", "reason": "scans"},
	{"action": "someone/installs", "called_by": ["someone/scans"], "reason": "run by the scan"},
]

# fetched is what the pinned actions run, as the fetch writes it.
fetched(by) := {"path": "/tmp/offline/pinned-actions.json", "contents": {"pinned_actions": by}}

# Each ref is made first: OPA cannot take a template string written as an
# object's key, and stops.
at(name) := $"{name}@{sha}"

runs_of := {
	at("actions/fetch"): [],
	at("someone/scans"): [at("someone/installs")],
	at("someone/installs"): [],
}

says(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_every_action_listed_and_used_passes if {
	count(supplier_actions.deny) == 0 with input as [list(listed), used, fetched(runs_of)]
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

test_an_action_by_its_path_is_named_by_it if {
	paths := array.concat(listed, [{"action": "github/scans", "paths": [".", "start"], "reason": "scans"}])
	g := object.union(runs_of, {at("github/scans"): [], at("github/scans/start"): []})
	count(supplier_actions.deny) == 0 with input as [list(paths), workflow([$"actions/fetch@{sha}", $"someone/scans@{sha}", $"github/scans@{sha}", $"github/scans/start@{sha}"]), fetched(g)]
}

test_a_path_the_entry_does_not_name_is_refused if {
	says(supplier_actions.deny, "a workflow uses github/scans/finish, which is not in the approved suppliers list") with input as [list(array.concat(listed, [{"action": "github/scans", "paths": ["start"], "reason": "scans"}])), workflow([$"actions/fetch@{sha}", $"someone/scans@{sha}", $"github/scans/start@{sha}", $"github/scans/finish@{sha}"])]
}

test_a_path_nothing_uses_is_refused if {
	says(supplier_actions.deny, "names github/scans/finish, which no workflow uses") with input as [list(array.concat(listed, [{"action": "github/scans", "paths": ["start", "finish"], "reason": "scans"}])), workflow([$"actions/fetch@{sha}", $"someone/scans@{sha}", $"github/scans/start@{sha}"])]
}

test_nothing_fetched_is_refused if {
	says(supplier_actions.deny, "nothing says what the pinned actions run") with input as [list(listed), used]
}

test_a_pinned_action_not_fetched_is_refused if {
	says(supplier_actions.deny, $"actions/fetch@{sha} is pinned here and was not fetched") with input as [list(listed), used, fetched(object.remove(runs_of, [at("actions/fetch")]))]
}

test_an_action_a_pinned_one_runs_unlisted_is_refused if {
	g := object.union(runs_of, {at("actions/fetch"): [at("actions/cache/restore")]})
	says(supplier_actions.deny, "actions/fetch runs actions/cache/restore, which is not in the approved suppliers list") with input as [list(listed), used, fetched(g)]
}

test_a_called_by_the_commit_pinned_does_not_bear_out_is_refused if {
	g := object.union(runs_of, {at("someone/scans"): []})
	says(supplier_actions.deny, "says someone/scans runs someone/installs, and none does at the commit pinned") with input as [list(listed), used, fetched(g)]
}

test_a_caller_that_runs_none_of_an_entry_is_refused if {
	entry := {"action": "someone/installs", "called_by": ["someone/scans", "actions/fetch"], "reason": "run by both"}
	l := array.concat([e | some e in listed; e.action != "someone/installs"], [entry])
	says(supplier_actions.deny, "says actions/fetch runs someone/installs, and it runs none of it at the commit pinned") with input as [list(l), used, fetched(runs_of)]
}

test_each_path_run_by_one_of_its_callers_passes if {
	entry := {"action": "someone/installs", "paths": [".", "restore"], "called_by": ["someone/scans", "actions/fetch"], "reason": "run by both"}
	l := array.concat([e | some e in listed; e.action != "someone/installs"], [entry])
	g := object.union(runs_of, {at("actions/fetch"): [at("someone/installs/restore")], at("someone/installs/restore"): []})
	count(supplier_actions.deny) == 0 with input as [list(l), used, fetched(g)]
}
