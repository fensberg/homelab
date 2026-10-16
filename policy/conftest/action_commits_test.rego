package action_commits_test

import data.action_commits
import rego.v1

sha(c) := concat("", [c, "0123456789abcdef0123456789abcdef012345"])

wf(path, steps) := {"path": path, "contents": {"jobs": {"j": {"steps": steps}}}}

# Enough files and pinned paths that the floors stand aside, so each test
# shows one rule.
floor := [wf(sprintf(".github/workflows/f%d.yml", [i]), [{"uses": sprintf("o/r%d@%s", [i, sha("aa")])}, {"uses": "$/.github/workflows/mobilize"}]) | some i in numbers.range(1, 10)]

denies(files, words) if {
	some msg in action_commits.deny with input as files
	contains(msg, words)
}

test_each_action_written_once_passes if {
	count(action_commits.deny) == 0 with input as floor
}

test_a_second_copy_is_refused_whatever_it_names if {
	files := array.concat(floor, [wf(".github/workflows/again.yml", [{"uses": sprintf("o/r1@%s", [sha("aa")])}])])
	denies(files, "o/r1 is written in 2 places")
}

test_two_commits_of_one_repository_are_refused if {
	files := array.concat(floor, [
		wf(".github/workflows/a.yml", [{"uses": sprintf("github/codeql-action/init@%s", [sha("bb")])}]),
		wf(".github/workflows/b.yml", [{"uses": sprintf("github/codeql-action/analyze@%s", [sha("cc")])}]),
	])
	denies(files, "github/codeql-action is pinned to 2 different commits")
}


test_a_reading_that_found_nothing_is_refused if {
	denies([], "workflow or action file(s) were read")
	denies([], "no $/ reference was found anywhere")
}
