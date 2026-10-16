package taskfile_modules_test

import data.taskfile_modules
import rego.v1

taskfile(cmds) := [{"path": "taskfile.yml", "contents": {"tasks": {"test": {"cmds": cmds}}}}]

test_a_sweep_passes if {
	count(taskfile_modules.deny) == 0 with input as taskfile(["for dir in scripts/*/; do\n  go test -C \"$dir\" ./...\ndone", "go build -C scripts/clerk -o toolshed/clerk ."])
}

test_a_named_module_is_refused if {
	some msg in taskfile_modules.deny with input as taskfile(["go test -C scripts/contractor ./..."])
	contains(msg, "vets or tests named modules: go test -C scripts/contractor ./...")
}

test_a_comment_inside_a_script_is_not_the_script if {
	count(taskfile_modules.deny) == 0 with input as taskfile(["# it used to run go vet -C scripts/contractor\ngo vet ./..."])
}

test_no_taskfile_is_refused if {
	some msg in taskfile_modules.deny with input as []
	contains(msg, "was not read")
}
