# Every job hardens the runner first, wherever it runs. The other laws every
# job is held to are its bound (rules.job_timeout) and its checkouts
# (rules.job_checkouts).
package rules.job_hardening

import data.lib
import rego.v1

harden := "step-security/harden-runner@"

deny contains msg if {
	count(lib.jobs) == 0
	msg := "found no jobs in any workflow, so no job was checked for hardening"
}

# A job hardens the runner first. Every step before harden-runner runs
# unmonitored, so first is the whole property - either directly, or as the
# first step of a composite action from this repository.
deny contains msg if {
	some [file, name, job] in lib.jobs
	count(object.get(job, "steps", [])) == 0
	not job.uses
	msg := $"{file}: job \"{name}\" has no steps"
}

deny contains msg if {
	some [file, name, job] in lib.jobs
	first := object.get(job.steps[0], "uses", "")
	not contains(first, harden)
	not lib.same_repo_action(first)
	msg := $"{file}: job \"{name}\" does not begin with harden-runner (its first step is \"{ident(job.steps[0])}\"). Every step before harden-runner runs unmonitored, so it must be the first step, not merely present - directly, or as the first step of a composite action from this repository referenced with $/."
}

deny contains msg if {
	some [file, name, job] in lib.jobs
	first := object.get(job.steps[0], "uses", "")
	dir := lib.same_repo_action(first)
	not lib.steps_of(dir)
	msg := $"{file}: job \"{name}\" begins with {first}, and that action cannot be read: a job whose first step is an action nobody can inspect is a job nobody can say is hardened."
}

deny contains msg if {
	some [file, name, job] in lib.jobs
	first := object.get(job.steps[0], "uses", "")
	dir := lib.same_repo_action(first)
	inner := lib.steps_of(dir)
	not starts_hardened(inner)
	msg := $"{file}: job \"{name}\" begins with {first}, whose own first step is \"{inner_ident(inner)}\" rather than harden-runner. A job may start with an action from this repository only when that action hardens the runner before anything else."
}

starts_hardened(steps) if contains(object.get(steps[0], "uses", ""), harden)

inner_ident(steps) := ident(steps[0]) if count(steps) > 0

inner_ident(steps) := "(no steps)" if count(steps) == 0

ident(step) := step.uses

ident(step) := step.name if not step.uses

ident(step) := "(unnamed step)" if {
	not step.uses
	not step.name
}
