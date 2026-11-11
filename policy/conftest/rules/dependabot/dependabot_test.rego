package rules.dependabot_test

import data.rules.dependabot
import rego.v1

group := {"applies-to": "version-updates", "patterns": ["*"], "update-types": ["minor", "patch"]}

every_dependency := [{"dependency-type": "all"}]

config(groups) := [{"path": ".github/dependabot.yml", "contents": {"updates": [{"package-ecosystem": "gomod", "directory": "/x", "allow": every_dependency, "groups": groups}]}}]

several(entry) := [{"path": ".github/dependabot.yml", "contents": {"updates": [object.union({"package-ecosystem": "gomod", "directories": ["/x", "/y"]}, entry)]}}]

test_a_grouped_ecosystem_passes if {
	count(dependabot.deny) == 0 with input as config({"go": group})
}

test_no_ecosystems_is_refused if {
	some msg in dependabot.deny with input as [{"path": ".github/dependabot.yml", "contents": {"version": 2}}]
	contains(msg, "declares no ecosystems")
}

test_no_groups_is_refused if {
	some msg in dependabot.deny with input as config({})
	contains(msg, "gomod in /x declares no groups")
}

test_security_updates_in_the_group_is_refused if {
	some msg in dependabot.deny with input as config({"go": object.union(group, {"applies-to": "security-updates"})})
	contains(msg, `want "version-updates"`)
}

test_a_missing_applies_to_is_refused if {
	some msg in dependabot.deny with input as config({"go": object.remove(group, ["applies-to"])})
	contains(msg, `want "version-updates"`)
}

test_a_narrow_pattern_is_refused if {
	some msg in dependabot.deny with input as config({"go": object.union(group, {"patterns": ["golang.org/*"]})})
	contains(msg, `want ["*"]`)
}

test_no_update_types_is_refused if {
	some msg in dependabot.deny with input as config({"go": object.remove(group, ["update-types"])})
	contains(msg, "names no update-types")
}

test_a_major_is_refused if {
	some msg in dependabot.deny with input as config({"go": object.union(group, {"update-types": ["major", "minor"]})})
	contains(msg, `includes update-type "major"`)
}

test_an_entry_of_several_directories_is_held_like_one if {
	some msg in dependabot.deny with input as several({"allow": every_dependency})
	contains(msg, "gomod in /x, /y declares no groups")
}

test_a_go_entry_that_takes_only_direct_dependencies_is_refused if {
	some msg in dependabot.deny with input as several({"groups": {"go": group}})
	contains(msg, "gomod in /x, /y bumps only what a go.mod requires directly")
}

test_another_ecosystem_need_not_take_every_dependency if {
	count(dependabot.deny) == 0 with input as [{"path": ".github/dependabot.yml", "contents": {"updates": [{"package-ecosystem": "npm", "directory": "/", "groups": {"js": group}}]}}]
}
