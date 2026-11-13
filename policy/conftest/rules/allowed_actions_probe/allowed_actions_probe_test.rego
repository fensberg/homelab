package rules.allowed_actions_probe_test

import data.rules.allowed_actions_probe
import rego.v1

sha := "0123456789012345678901234567890123456789"

suppliers := {"path": "scripts/approved-suppliers.yml", "contents": {
	"actions_probe": "converge.yml/probe",
	"actions": [
		{"action": "actions/fetch"},
		{"action": "github/scan", "paths": ["start", "finish"]},
	],
}}

never := "github.run_attempt == '0'"

probe_steps := [
	{"name": "fetch", "uses": $"actions/fetch@{sha}", "if": never},
	{"name": "start", "uses": $"github/scan/start@{sha}", "if": never},
	{"name": "finish", "uses": $"github/scan/finish@{sha}", "if": never},
]

workflow(probe) := {"path": ".github/workflows/converge.yml", "contents": {"jobs": {
	"converge": {"environment": "estate", "steps": [{"run": "converge"}]},
	"probe": probe,
}}}

good_probe := {"needs": "converge", "steps": probe_steps}

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_a_probe_of_every_allowed_action_that_runs_nothing_passes if {
	count(allowed_actions_probe.deny) == 0 with input as [suppliers, workflow(good_probe)]
	count(allowed_actions_probe.deny) == 0 with input as [suppliers, workflow({"needs": ["converge"], "steps": probe_steps})]
}

test_an_environment_written_with_its_name_is_read if {
	named := {"path": ".github/workflows/converge.yml", "contents": {"jobs": {
		"converge": {"environment": {"name": "estate"}, "steps": [{"run": "converge"}]},
		"probe": good_probe,
	}}}
	count(allowed_actions_probe.deny) == 0 with input as [suppliers, named]
}

test_no_probe_is_refused if {
	denies(allowed_actions_probe.deny, "no job proves the allowed-actions setting") with input as [suppliers]
	denies(allowed_actions_probe.deny, "no job proves") with input as [object.union(suppliers, {"contents": {"actions_probe": "converge.yml/other"}}), workflow(good_probe)]
}

test_an_allowed_action_the_probe_misses_is_refused if {
	denies(allowed_actions_probe.deny, "does not reference github/scan/finish") with input as [suppliers, workflow({"needs": "converge", "steps": array.slice(probe_steps, 0, 2)})]
}

test_an_action_the_setting_does_not_allow_is_refused if {
	extra := array.concat(probe_steps, [{"name": "x", "uses": $"someone/else@{sha}", "if": never}])
	denies(allowed_actions_probe.deny, "references someone/else, which the setting does not allow") with input as [suppliers, workflow({"needs": "converge", "steps": extra})]
}

test_a_step_not_at_a_full_commit_is_refused if {
	loose := array.concat(probe_steps, [{"name": "tag", "uses": "actions/fetch@v4", "if": never}])
	denies(allowed_actions_probe.deny, "step 3 does not reference an action at a full commit") with input as [suppliers, workflow({"needs": "converge", "steps": loose})]
	ran := array.concat(probe_steps, [{"run": "echo", "if": never}])
	denies(allowed_actions_probe.deny, "step 3 does not reference an action at a full commit") with input as [suppliers, workflow({"needs": "converge", "steps": ran})]
}

test_a_step_that_runs_or_is_given_something_is_refused if {
	given := array.concat(probe_steps, [{"name": "w", "uses": $"actions/fetch@{sha}", "if": never, "with": {"a": 1}}])
	denies(allowed_actions_probe.deny, "step 3 has with") with input as [suppliers, workflow({"needs": "converge", "steps": given})]
}

test_a_step_under_any_other_condition_is_refused if {
	bare := {"name": "f", "uses": $"actions/fetch@{sha}"}
	every step in [bare, object.union(bare, {"if": false}), object.union(bare, {"if": "false"}), object.union(bare, {"if": "github.run_attempt == '1'"})] {
		denies(allowed_actions_probe.deny, "step 3 does not have the condition") with input as [suppliers, workflow({"needs": "converge", "steps": array.concat(probe_steps, [step])})]
	}
}

test_a_probe_with_a_condition_of_its_own_is_refused if {
	denies(allowed_actions_probe.deny, "has a condition of its own") with input as [suppliers, workflow(object.union(good_probe, {"if": "success()"}))]
}

test_a_probe_that_does_not_follow_the_converge_is_refused if {
	denies(allowed_actions_probe.deny, "does not need the job that converges the estate") with input as [suppliers, workflow({"steps": probe_steps})]
	elsewhere := {"path": ".github/workflows/converge.yml", "contents": {"jobs": {
		"converge": {"environment": "management", "steps": [{"run": "converge"}]},
		"probe": good_probe,
	}}}
	denies(allowed_actions_probe.deny, "does not need the job that converges the estate") with input as [suppliers, elsewhere]
}
