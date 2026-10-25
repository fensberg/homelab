package rules.plan_allowed_actions_test

import data.rules.plan_allowed_actions
import rego.v1

sha := "0123456789012345678901234567890123456789"

wall := {
	"allowed_actions": "selected",
	"sha_pinning_required": true,
	"allowed_actions_config": [{"github_owned_allowed": true, "verified_allowed": false, "patterns_allowed": ["someone/builds@*"]}],
}

tf(setting) := {"path": "estate.plan.json", "contents": {"planned_values": {"root_module": {"child_modules": [{"resources": [{"address": "module.repository[\"r\"].github_actions_repository_permissions.this", "type": "github_actions_repository_permissions", "values": setting}, {"address": "something_else.ignored", "type": "something_else", "values": {"allowed_actions": "all"}}]}]}}}}

uses := [sprintf("actions/fetch@%s", [sha]), sprintf("github/scan/start@%s", [sha]), sprintf("someone/builds@%s", [sha]), sprintf("actions/two@%s", [sha]), sprintf("github/three@%s", [sha]), "./.github/workflows/local", "$/.github/workflows/shared", "docker://example.test/image:1"]

workflow(refs) := {"path": ".github/workflows/w.yml", "contents": {"jobs": {"a": {"steps": [{"uses": r} | some r in refs]}}}}

says(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_the_actions_used_are_read_by_repository if {
	plan_allowed_actions.used == {"actions/fetch", "github/scan", "someone/builds", "actions/two", "github/three"} with input as [workflow(uses)]
}

test_a_wall_that_holds_passes if {
	count(plan_allowed_actions.deny) == 0 with input as [tf(wall), workflow(uses)]
}

test_any_action_is_refused if says(plan_allowed_actions.deny, "does not hold workflows to a list") with input as [tf(object.union(wall, {"allowed_actions": "all"})), workflow(uses)]

test_a_list_worked_out_elsewhere_is_refused if says(plan_allowed_actions.deny, "does not hold workflows to a list") with input as [tf(object.union(wall, {"allowed_actions": "${var.which}"})), workflow(uses)]

test_actions_from_a_tag_are_refused if says(plan_allowed_actions.deny, "from a tag or a branch") with input as [tf(object.union(wall, {"sha_pinning_required": false})), workflow(uses)]

test_pinning_left_unsaid_is_refused if says(plan_allowed_actions.deny, "from a tag or a branch") with input as [tf(object.remove(wall, ["sha_pinning_required"])), workflow(uses)]

test_verified_publishers_are_refused if {
	says(plan_allowed_actions.deny, "every publisher the platform has verified") with input as [tf(object.union(wall, {"allowed_actions_config": [{"github_owned_allowed": true, "verified_allowed": true, "patterns_allowed": ["someone/builds@*"]}]})), workflow(uses)]
}

test_verified_left_unsaid_is_refused if {
	says(plan_allowed_actions.deny, "every publisher the platform has verified") with input as [tf(object.union(wall, {"allowed_actions_config": [{"github_owned_allowed": true, "patterns_allowed": ["someone/builds@*"]}]})), workflow(uses)]
}

test_a_whole_organisation_is_refused if {
	says(plan_allowed_actions.deny, "wider than one action") with input as [tf(object.union(wall, {"allowed_actions_config": [{"github_owned_allowed": true, "verified_allowed": false, "patterns_allowed": ["someone/*"]}]})), workflow(uses)]
}

test_an_action_it_would_refuse_is_named if says(plan_allowed_actions.deny, "uses another/thing, which it would refuse") with input as [tf(wall), workflow(array.concat(uses, [sprintf("another/thing@%s", [sha])]))]

test_the_platforms_own_unwanted_is_refused if {
	says(plan_allowed_actions.deny, "uses actions/fetch, which it would refuse") with input as [tf(object.union(wall, {"allowed_actions_config": [{"github_owned_allowed": false, "verified_allowed": false, "patterns_allowed": ["someone/builds@*"]}]})), workflow(uses)]
}

test_no_setting_at_all_is_refused if {
	some msg in plan_allowed_actions.deny with input as [workflow(uses)]
	contains(msg, "no github_actions_repository_permissions is in any plan")
}
