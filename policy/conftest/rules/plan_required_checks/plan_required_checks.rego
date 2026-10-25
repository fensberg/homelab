# Every check a merge is held to is the name of a job some workflow runs.
#
# WHY. The estate sets which checks a change has to pass, by name. The
# platform waits for a check of that name and has no way to know nobody
# will ever report one: a name mistyped, or a job renamed in a workflow and
# not here, holds every pull request for ever. Nobody may pass the checks
# by, so the way out of that is converging the estate by hand.
#
# FOUND BY WHAT IT IS. The checks are every one a ruleset in the estate's
# plan requires, as it would be applied, and the names are every job of
# every workflow.
package rules.plan_required_checks

import data.lib
import rego.v1

# checks is every check a ruleset holds a merge to, as the estate's plan
# would apply it: each required check's name, in every ruleset of every
# repository.
checks contains c.context if {
	some [_, r] in lib.plan
	r.type == "github_repository_ruleset"
	some rule in object.get(r.values, "rules", [])
	some required in object.get(rule, "required_status_checks", [])
	some c in object.get(required, "required_check", [])
	is_string(c.context)
}

reported contains object.get(job, "name", name) if {
	some [_, name, job] in lib.jobs
}

deny contains msg if {
	count(checks) == 0
	msg := "no ruleset in any plan holds a merge to a check, so nothing here says which checks a merge is held to"
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
