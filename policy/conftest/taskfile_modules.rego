# The taskfile sweeps every Go module rather than naming any.
#
# A named module is a list, and a list is the thing that went stale: clerk
# and inspector were left off three of them and were never compiled or run by
# anything. This does not check that the sweep is correct - `task validate`
# and `task test` do that by running - it checks that a sweep is what is
# there. Building a specific tool in order to run it is fine and common -
# `task clear-branches` does it - so only the verbs that ought to sweep every
# module are refused.
package taskfile_modules

import rego.v1

taskfile := [f | some f in input; f.path == "taskfile.yml"]

named contains trim_space(line) if {
	some f in taskfile
	walk(f.contents, [_, v])
	is_string(v)
	some line in split(v, "\n")
	not startswith(trim_space(line), "#")
	regex.match(`go (vet|test) -C scripts/[a-z]`, line)
}

deny contains msg if {
	count(taskfile) == 0
	msg := "taskfile.yml was not read, so whether it sweeps the modules is not known"
}

deny contains msg if {
	count(named) > 0
	msg := sprintf("taskfile.yml vets or tests named modules: %s. That is a list, and a list is what went stale: clerk and inspector were left off three of them and were never compiled or run by anything. Sweep scripts/*/go.mod instead, so a module added tomorrow is covered the moment it exists.", [concat("; ", sort(named))])
}
