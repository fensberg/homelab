# Every check a merge is held to is the name of a job some workflow runs.
#
# WHY. The estate sets which checks a change has to pass, by name. The
# platform waits for a check of that name and has no way to know nobody
# will ever report one: a name mistyped, or a job renamed in a workflow and
# not here, holds every pull request for ever. Nobody may pass the checks
# by, so the way out of that is converging the estate by hand.
#
# FOUND BY WHAT IT IS. The list is whatever is declared as the checks
# required, wherever it is, and the names are every job of every workflow.
package required_checks

import data.lib
import rego.v1

declared := "required_checks"

# lists is every list declared under that name in a locals block; one worked
# out from something else is not a list here, and is not read.
lists contains [f.path, list] if {
	some f in input
	endswith(f.path, ".tf")
	some block in object.get(f.contents, "locals", [])
	list := block[declared]
	is_array(list)
}

checks contains c if {
	some [_, list] in lists
	some c in list
	is_string(c)
}

reported contains object.get(job, "name", name) if {
	some [_, name, job] in lib.jobs
}

deny contains msg if {
	count(checks) == 0
	msg := sprintf("nothing declares %s, so nothing here says which checks a merge is held to", [declared])
}

deny contains msg if {
	count(lib.workflows) == 0
	msg := "no workflow was read, so this checked nothing"
}

deny contains msg if {
	some c in checks
	not c in reported
	msg := sprintf("a merge is held to a check named %q, and no job in any workflow reports under that name. Every pull request would wait on it for ever. Name the job as it is, or take the check out.", [c])
}
