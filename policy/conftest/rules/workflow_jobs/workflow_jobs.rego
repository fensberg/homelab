# The laws every job is held to, wherever it runs.
package rules.workflow_jobs

import data.lib
import rego.v1

harden := "step-security/harden-runner@"

deny contains msg if {
	count(lib.jobs) == 0
	msg := "found no jobs in any workflow, so no job law was checked"
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

# A job is bounded. Without timeout-minutes it inherits GitHub's six hours, in
# which a stuck job keeps calling whatever it can reach, metered vendors too.
deny contains msg if {
	some [file, name, job] in lib.jobs
	not bounded(job)
	msg := $"{file}: job \"{name}\" declares no timeout-minutes, so it inherits GitHub's default of 360 - six hours in which a stuck job keeps calling whatever it can reach, including metered vendors. Give it a bound comfortably above its normal runtime."
}

bounded(job) if {
	is_number(job["timeout-minutes"])
	job["timeout-minutes"] > 0
}

# Every checkout refuses to persist credentials, in the job and inside every
# composite action it reaches: a token left on disk is reachable by every
# later step of the job, wherever the checkout was written.
#
# Rego has no recursion, so the actions a job reaches are followed one level
# at a time, four deep; one that reaches further, or loops back on itself, is
# refused rather than passed unread.
level0 contains [file, name, "", step] if {
	some [file, name, job] in lib.jobs
	some step in object.get(job, "steps", [])
}

level1 contains entry if {
	some parent in level0
	some entry in inside(parent)
}

level2 contains entry if {
	some parent in level1
	some entry in inside(parent)
}

level3 contains entry if {
	some parent in level2
	some entry in inside(parent)
}

level4 contains entry if {
	some parent in level3
	some entry in inside(parent)
}

inside(parent) := {[parent[0], parent[1], $"{parent[2]} (inside {parent[3].uses})", inner] |
	dir := lib.same_repo_action(object.get(parent[3], "uses", ""))
	some inner in lib.steps_of(dir)
}

reached := ((level0 | level1) | level2) | level3

checkouts contains entry if {
	some entry in reached
	contains(object.get(entry[3], "uses", ""), "actions/checkout@")
}

deny contains msg if {
	count(checkouts) == 0
	msg := "no checkout was found in any job, at any depth, so the checkout law checked nothing"
}

deny contains msg if {
	some [file, name, where, step] in checkouts
	object.get(object.get(step, "with", {}), "persist-credentials", null) != false
	msg := $"{file}: job \"{name}\" checks out without persist-credentials: false{where}. The job's token stays on disk for every later step otherwise, and anything one of those steps runs can push with it."
}

deny contains msg if {
	some [file, name, where, step] in level4
	msg := $"{file}: job \"{name}\" reaches this repository's actions more than four deep{where}, or round in a loop, so what it checks out cannot be read"
}

deny contains msg if {
	some [file, name, where, step] in reached
	uses := object.get(step, "uses", "")
	dir := lib.same_repo_action(uses)
	not lib.actions[dir]
	msg := $"{file}: job \"{name}\" uses {uses}{where}, which cannot be read"
}
