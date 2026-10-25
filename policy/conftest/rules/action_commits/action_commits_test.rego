package rules.action_commits_test

import data.rules.action_commits
import rego.v1

sha(c) := concat("", [c, "0123456789abcdef0123456789abcdef012345"])

wf(path, steps) := {"path": path, "contents": {"jobs": {"j": {"steps": steps}}}}

# Enough files and pinned paths that the floors stand aside, so each test
# shows one rule.
pinned_files := [wf(sprintf(".github/workflows/f%d.yml", [i]), [{"uses": sprintf("o/r%d@%s", [i, sha("aa")])}, {"uses": "$/.github/workflows/mobilize"}]) | some i in numbers.range(1, 10)]

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_each_action_written_once_passes if {
	count(action_commits.deny) == 0 with input as pinned_files
}

test_a_second_copy_is_refused_whatever_it_names if {
	files := array.concat(pinned_files, [wf(".github/workflows/again.yml", [{"uses": sprintf("o/r1@%s", [sha("aa")])}])])
	denies(action_commits.deny, "o/r1 is written in 2 places") with input as files
}

test_two_commits_of_one_repository_are_refused if {
	files := array.concat(pinned_files, [
		wf(".github/workflows/a.yml", [{"uses": sprintf("github/codeql-action/init@%s", [sha("bb")])}]),
		wf(".github/workflows/b.yml", [{"uses": sprintf("github/codeql-action/analyze@%s", [sha("cc")])}]),
	])
	denies(action_commits.deny, "github/codeql-action is pinned to 2 different commits") with input as files
}

test_a_reading_that_found_nothing_is_refused if {
	denies(action_commits.deny, "workflow or action file(s) were read") with input as []
	denies(action_commits.deny, "no $/ reference was found anywhere") with input as []
}
