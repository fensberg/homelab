package allowed_actions_test

import data.allowed_actions
import rego.v1

sha := "0123456789012345678901234567890123456789"

wall := {
	"allowed_actions": "selected",
	"sha_pinning_required": true,
	"allowed_actions_config": [{"github_owned_allowed": true, "verified_allowed": false, "patterns_allowed": ["someone/builds@*"]}],
}

tf(setting) := {"path": "made-up.tf", "contents": {"resource": {"github_actions_repository_permissions": {"one": [setting]}, "something_else": {"ignored": [{"allowed_actions": "all"}]}}}}

uses := [sprintf("actions/fetch@%s", [sha]), sprintf("github/scan/start@%s", [sha]), sprintf("someone/builds@%s", [sha]), sprintf("actions/two@%s", [sha]), sprintf("github/three@%s", [sha]), "./.github/workflows/local", "$/.github/workflows/shared", "docker://example.test/image:1"]

workflow(refs) := {"path": ".github/workflows/w.yml", "contents": {"jobs": {"a": {"steps": [{"uses": r} | some r in refs]}}}}

says(setting, refs, words) if {
	some msg in allowed_actions.deny with input as [tf(setting), workflow(refs)]
	contains(msg, words)
}

test_the_actions_used_are_read_by_repository if {
	allowed_actions.used == {"actions/fetch", "github/scan", "someone/builds", "actions/two", "github/three"} with input as [workflow(uses)]
}

test_a_wall_that_holds_passes if {
	count(allowed_actions.deny) == 0 with input as [tf(wall), workflow(uses)]
}

test_any_action_is_refused if says(object.union(wall, {"allowed_actions": "all"}), uses, "does not hold workflows to a list")

test_a_list_worked_out_elsewhere_is_refused if says(object.union(wall, {"allowed_actions": "${var.which}"}), uses, "does not hold workflows to a list")

test_actions_from_a_tag_are_refused if says(object.union(wall, {"sha_pinning_required": false}), uses, "from a tag or a branch")

test_pinning_left_unsaid_is_refused if says(object.remove(wall, ["sha_pinning_required"]), uses, "from a tag or a branch")

test_verified_publishers_are_refused if {
	says(object.union(wall, {"allowed_actions_config": [{"github_owned_allowed": true, "verified_allowed": true, "patterns_allowed": ["someone/builds@*"]}]}), uses, "every publisher the platform has verified")
}

test_verified_left_unsaid_is_refused if {
	says(object.union(wall, {"allowed_actions_config": [{"github_owned_allowed": true, "patterns_allowed": ["someone/builds@*"]}]}), uses, "every publisher the platform has verified")
}

test_a_whole_organisation_is_refused if {
	says(object.union(wall, {"allowed_actions_config": [{"github_owned_allowed": true, "verified_allowed": false, "patterns_allowed": ["someone/*"]}]}), uses, "wider than one action")
}

test_an_action_it_would_refuse_is_named if says(wall, array.concat(uses, [sprintf("another/thing@%s", [sha])]), "uses another/thing, which it would refuse")

test_the_platforms_own_unwanted_is_refused if {
	says(object.union(wall, {"allowed_actions_config": [{"github_owned_allowed": false, "verified_allowed": false, "patterns_allowed": ["someone/builds@*"]}]}), uses, "uses actions/fetch, which it would refuse")
}

test_no_setting_at_all_is_refused if {
	some msg in allowed_actions.deny with input as [workflow(uses)]
	contains(msg, "no github_actions_repository_permissions is declared")
}
