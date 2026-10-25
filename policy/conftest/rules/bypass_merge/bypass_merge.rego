# Every merge under a bypass is judged where it merges.
#
# `gh pr merge --admin` skips review and required checks alike, so the commit
# judged must be the commit merged (--match-head-commit), and the job that
# merges must judge the standing order itself: the Sensitive Paths check
# cannot bound a merge the bypass skips.
package rules.bypass_merge

import data.lib
import rego.v1

merges contains [file, name, line, judged] if {
	some [file, name, job] in lib.jobs
	scripts := [regex.replace(object.get(s, "run", ""), `\\\n\s*`, " ") | some s in object.get(job, "steps", [])]
	judged := count([1 | some sc in scripts; contains(sc, "enforce-standing-order")]) > 0
	some sc in scripts
	some l in split(sc, "\n")
	contains(l, "gh pr merge")
	contains(l, "--admin")
	line := trim_space(l)
}

deny contains msg if {
	count(merges) == 0
	msg := "no workflow merges with `gh pr merge --admin`, so the bypass rule checked nothing - either the expedite release stopped using the bypass or the reading stopped matching it"
}

deny contains msg if {
	some [file, name, line, _] in merges
	not contains(line, "--match-head-commit")
	msg := $"{file} job {name} merges under a bypass without --match-head-commit:\n  {line}\nA bypass skips review and required checks alike, so the commit judged must be the commit merged."
}

deny contains msg if {
	some [file, name, _, false] in merges
	msg := $"{file} job {name} merges under a bypass and never runs enforce-standing-order. The Sensitive Paths check cannot bound this merge: the bypass skips it. The job that merges must judge the standing order itself, against the head it merges."
}
