package rules.toolshed_test

import data.rules.toolshed
import rego.v1

taskfile(cmds) := {"path": "taskfile.yml", "contents": {"tasks": {"t": {"cmds": cmds}}}}

ignore(paths) := {"path": ".gitignore", "contents": array.concat([{"Kind": "Comment", "Value": "toolshed/"}], [{"Kind": "Path", "Value": p} | some p in paths])}

test_a_build_into_the_toolshed_passes if {
	count(toolshed.deny) == 0 with input as [taskfile(["go build -C scripts/signedpush -o ../../toolshed/signedpush .", "go build -C scripts/x -o /dev/null ."]), ignore(["toolshed/"])]
}

test_a_build_beside_its_source_is_refused if {
	some msg in toolshed.deny with input as [taskfile(["go build -C scripts/signedpush -o signedpush ."]), ignore(["toolshed/"])]
	contains(msg, `builds a binary to "signedpush", outside the toolshed`)
}

test_no_build_is_refused if {
	some msg in toolshed.deny with input as [taskfile(["go build -C scripts/x -o /dev/null ."]), ignore(["toolshed/"])]
	contains(msg, "no builds found")
}

test_a_toolshed_named_only_in_a_comment_is_not_ignored if {
	some msg in toolshed.deny with input as [taskfile(["go build -C scripts/a -o ../../toolshed/a ."]), ignore([".terraform/"])]
	contains(msg, "toolshed/ is not ignored")
}
