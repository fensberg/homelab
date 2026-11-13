# The repository's allowed-actions setting is proved by the platform itself,
# straight after the estate converge applies it (#882).
#
# A pull request that changes the setting cannot prove it with its own checks:
# they run under the setting already applied, and the new one first applies
# when the estate converges after the merge. If it refuses an action a
# workflow needs, every job that needs it fails from then on (#878).
#
# The platform checks every action a job references while setting the job up,
# before any step runs - a step that will be skipped and an action nested in
# another included. So a job that references every allowed action, each in a
# step that never runs, is refused exactly when the setting refuses one of
# them: the same operation that relies on the setting, at the moment it
# changes. The suppliers list declares which job that is, under
# actions_probe; this holds it to being that job:
#
#   - it references every action the setting allows, and nothing else, each
#     at a full commit. Any commit of an action proves it, as the setting
#     allows the action at every commit, so the probe's commits are witnesses
#     and need not move when a workflow's do;
#   - it runs nothing: every step only references an action, under the one
#     condition that is never true - github.run_attempt == '0', as a run's
#     first attempt is 1 - worked out when the job runs, so the platform
#     cannot drop the step before setting the job up; the job itself has no
#     condition;
#   - it follows the job that converges the estate - the one given the
#     estate's environment.
package rules.allowed_actions_probe

import data.lib
import rego.v1

suppliers := [f.contents | some f in lib.at("scripts/approved-suppliers.yml")]

# allowed is every action the setting allows, named as a step names one: its
# repository, and the path in it when not at the root - as
# management/estate/repository/workflows.tf makes the setting's patterns.
allowed contains name if {
	some list in suppliers
	some a in object.get(list, "actions", [])
	some p in object.get(a, "paths", ["."])
	name := named(a.action, p)
}

named(action, ".") := action

named(action, p) := $"{action}/{p}" if p != "."

probe contains [file, name, job] if {
	some [file, name, job] in lib.jobs
	[file, name] == lib.actions_probe
}

steps contains [where, i, step] if {
	some [file, name, job] in probe
	where := $"{file} job {name}"
	some i, step in object.get(job, "steps", [])
}

witnessed contains split(step.uses, "@")[0] if {
	some [_, _, step] in steps
	is_string(step.uses)
}

pinned := `^[^@]+@[0-9a-f]{40}$`

# never is the one condition a probe's step has: a run's first attempt is 1.
never := "github.run_attempt == '0'"

deny contains msg if {
	count(probe) == 0
	msg := "no job proves the allowed-actions setting after the estate converges: the suppliers list's actions_probe names no job of a workflow, as <workflow file>/<job>"
}

deny contains msg if {
	count(probe) > 0
	some name in allowed
	not name in witnessed
	msg := $"the allowed-actions probe does not reference {name}, so a setting that refuses it would pass the probe. Give it a step using {name} at any commit of it."
}

deny contains msg if {
	some name in witnessed
	not name in allowed
	msg := $"the allowed-actions probe references {name}, which the setting does not allow, so the probe would fail every converge. Take the step out, or declare {name} under actions: in the approved suppliers list."
}

deny contains msg if {
	some [where, i, step] in steps
	not regex.match(pinned, object.get(step, "uses", ""))
	msg := $"{where}: step {i} does not reference an action at a full commit. Each step of the probe only references an action, as owner/repository[/path]@<40-hex commit>."
}

deny contains msg if {
	some [where, i, step] in steps
	some key in object.keys(step)
	not key in {"name", "uses", "if"}
	msg := $"{where}: step {i} has {key}. The probe runs nothing and is given nothing: a step has a name, the action it references, and a condition."
}

deny contains msg if {
	some [where, i, step] in steps
	object.get(step, "if", null) != never
	msg := $"{where}: step {i} does not have the condition `{never}`, which is worked out when the job runs and is never true. Any other either runs the step, or may let the platform drop it before the job is set up."
}

deny contains msg if {
	some [file, name, job] in probe
	"if" in object.keys(job)
	msg := $"{file} job {name} has a condition of its own. The probe is only a probe when its job is set up; skipped, it proves nothing."
}

deny contains msg if {
	some [file, name, job] in probe
	not follows_the_converge(file, job)
	msg := $"{file} job {name} does not need the job that converges the estate - the one given the estate's environment - so it can run before the setting it proves is applied."
}

follows_the_converge(file, job) if {
	some need in needs(object.get(job, "needs", []))
	some [other_file, other_name, other] in lib.jobs
	other_file == file
	other_name == need
	environment(other) == "estate"
}

needs(n) := [n] if is_string(n)

needs(n) := n if is_array(n)

environment(job) := job.environment if is_string(job.environment)

environment(job) := job.environment.name if is_object(job.environment)
