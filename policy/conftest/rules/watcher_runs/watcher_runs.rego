# A workflow started by another takes nothing from it but its outcome: it
# runs with the default branch's token whoever started the run it watches.
# It is one of the ways a workflow is handed code nobody here reviewed; the
# others are a checkout's settings (rules.checkout_settings), a privileged
# run's workspace (rules.privileged_workspace) and a dispatched workflow's
# inputs (rules.dispatch_inputs).
package rules.watcher_runs

import data.lib
import rego.v1

watchers contains [file, doc] if {
	some file, doc in lib.workflows
	"workflow_run" in lib.triggers(doc)
}

all_steps(doc) := array.concat(
	[s | some job in object.get(doc, "jobs", {}); some s in object.get(job, "steps", [])],
	object.get(object.get(doc, "runs", {}), "steps", []),
)

tells_nothing_checked_out(doc) if {
	walk(doc, [where, "false"])
	count(where) > 0
	where[count(where) - 1] == "checkout"
}

deny contains msg if {
	some [file, doc] in watchers
	some step in all_steps(doc)
	regex.match(`(checkout|download-artifact)\b`, object.get(step, "uses", ""))
	msg := $"{file} checks out or downloads an artifact, so the watched run's code or output reaches a privileged runner"
}

deny contains msg if {
	some [file, doc] in watchers
	walk(doc, [_, v])
	is_string(v)
	some m in regex.find_all_string_submatch_n(`github\.event\.workflow_run\.([a-z_]+)`, v, -1)
	not m[1] in {"name", "conclusion", "event", "html_url"}
	msg := $"{file} reads the watched run's {m[1]}, which whoever started that run may have chosen. Read its name, conclusion, event and address and nothing else."
}

deny contains msg if {
	some [file, doc] in watchers
	not tells_nothing_checked_out(doc)
	msg := $"{file} does not tell its first step to check nothing out"
}

deny contains msg if {
	some [file, doc] in watchers
	not is_object(object.get(doc, "permissions", null))
	msg := $"{file} does not spell out its permissions"
}

deny contains msg if {
	some [file, doc] in watchers
	some scope, level in object.get(doc, "permissions", {})
	not level in {"read", "none"}
	scope != "issues"
	msg := $"{file} may {level} {scope}, and it runs with the default branch's token whoever started the run it watches"
}

deny contains msg if {
	some [file, doc] in watchers
	some name, job in object.get(doc, "jobs", {})
	perms := job.permissions
	some why in watcher_job_problems(perms)
	msg := $"{file}, in its job {name}, {why}"
}

watcher_job_problems(perms) := {"does not spell out its permissions"} if not is_object(perms)

watcher_job_problems(perms) := {$"may {level} {scope}" | some scope, level in perms; not level in {"read", "none"}; scope != "issues"} if is_object(perms)
