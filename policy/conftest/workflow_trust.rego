# What a workflow may do with code nobody here reviewed.
package rules.workflow_trust

import data.lib
import rego.v1

# A checkout may only be told how much history to fetch and not to keep the
# token. Anything that changes WHAT is checked out lets a caller place code
# where later steps execute, which in a run holding secrets is how the clerk
# handed a stranger's pull request its App's key (#409).
checkouts contains [file, step] if {
	some file, doc in lib.runnable
	some step in all_steps(doc)
	contains(object.get(step, "uses", ""), "actions/checkout@")
}

all_steps(doc) := array.concat(
	[s | some _, job in object.get(doc, "jobs", {}); some s in object.get(job, "steps", [])],
	object.get(object.get(doc, "runs", {}), "steps", []),
)

deny contains msg if {
	count(checkouts) == 0
	msg := "no actions/checkout step was found in any workflow or action, so the checkout rule checked nothing"
}

deny contains msg if {
	some [file, step] in checkouts
	some key, _ in object.get(step, "with", {})
	not key in {"fetch-depth", "persist-credentials"}
	msg := sprintf("%s gives actions/checkout %q. A checkout here may only be told how deep to fetch. Anything that changes WHAT is checked out lets a caller place code that was never reviewed where later steps execute (#409). To read other code, fetch it as data into its own worktree and do not run it, as clerk.yml does.", [file, key])
}

# A run on a trigger anybody's pull request reaches, holding secrets, never
# puts the pull request's code in the workspace, where every later step and
# every ./ action executes from.
privileged contains [file, doc] if {
	some file, doc in lib.workflows
	count(lib.triggers(doc) & {"issue_comment", "pull_request_target", "workflow_run"}) > 0
}

deny contains msg if {
	count(privileged) == 0
	msg := "no workflow runs on a privileged trigger, and clerk.yml does - the reading has stopped matching"
}

moves_workspace := `\bgit\b[^\n;&|]*\s(checkout|switch|reset|restore|pull|merge|read-tree|stash\s+apply)\b`

deny contains msg if {
	some [file, doc] in privileged
	some name, job in object.get(doc, "jobs", {})
	some step in object.get(job, "steps", [])
	run := object.get(step, "run", "")
	m := regex.find_n(moves_workspace, run, 1)[0]
	msg := sprintf("%s job %q step %q runs %q. This workflow runs with secrets on a trigger that anybody's pull request can reach, and that command replaces what is in the workspace - where every later step, and every ./ action, executes from. A pull request's code belongs in its own worktree, read and never run.", [file, name, object.get(step, "name", ""), trim_space(m)])
}

deny contains msg if {
	some [file, doc] in privileged
	some name, job in object.get(doc, "jobs", {})
	some step in object.get(job, "steps", [])
	run := object.get(step, "run", "")
	regex.match(`refs/pull/|pull_request\.head`, run)
	not contains(run, "git worktree add")
	msg := sprintf("%s job %q step %q fetches a pull request's code without its own worktree. In a run holding secrets, a pull request is data. Fetch it into a worktree of its own - `git worktree add --detach <dir> <commit>` - so nothing that executes from the workspace can be reached by it.", [file, name, object.get(step, "name", "")])
}

# A workflow that takes inputs when dispatched can publish nothing: whoever
# dispatches it would choose what goes into what it publishes.
dispatched(doc) := d if {
	on := object.get(doc, "on", object.get(doc, "true", {}))
	is_object(on)
	d := on.workflow_dispatch
	is_object(d)
	d.inputs
}

writes(perms) := {"declares no permissions, so it has whatever the repository's default is"} if perms == null

writes(perms) := set() if perms in {"read-all", {}}

writes(perms) := {sprintf("has permissions: %v", [perms])} if {
	is_string(perms)
	perms != "read-all"
}

writes(perms) := {sprintf("may %s %s", [level, scope]) | some scope, level in perms; not level in {"read", "none"}} if {
	is_object(perms)
	perms != {}
}

deny contains msg if {
	some file, doc in lib.workflows
	dispatched(doc)
	some why in writes(object.get(doc, "permissions", null))
	msg := sprintf("%s takes inputs and %s. Whoever dispatches it would choose what goes into what it publishes. Take the input away, or the write.", [file, why])
}

deny contains msg if {
	some file, doc in lib.workflows
	dispatched(doc)
	some name, job in object.get(doc, "jobs", {})
	job.permissions
	some why in writes(job.permissions)
	msg := sprintf("%s takes inputs and its job %s %s. Whoever dispatches it would choose what goes into what it publishes. Take the input away, or the write.", [file, name, why])
}

# A workflow started by another takes nothing from it but its outcome: it
# runs with the default branch's token whoever started the run it watches.
watchers contains [file, doc] if {
	some file, doc in lib.workflows
	"workflow_run" in lib.triggers(doc)
}

deny contains msg if {
	some [file, doc] in watchers
	walk(doc, [_, v])
	is_string(v)
	some m in regex.find_all_string_submatch_n(`github\.event\.workflow_run\.([a-z_]+)`, v, -1)
	not m[1] in {"name", "conclusion", "event", "html_url"}
	msg := sprintf("%s reads the watched run's %s, which whoever started that run may have chosen. Read its name, conclusion, event and address and nothing else.", [file, m[1]])
}

deny contains msg if {
	some [file, doc] in watchers
	some step in all_steps(doc)
	regex.match(`(checkout|download-artifact)\b`, object.get(step, "uses", ""))
	msg := sprintf("%s checks out or downloads an artifact, so the watched run's code or output reaches a privileged runner", [file])
}

deny contains msg if {
	some [file, doc] in watchers
	not tells_nothing_checked_out(doc)
	msg := sprintf("%s does not tell its first step to check nothing out", [file])
}

tells_nothing_checked_out(doc) if {
	walk(doc, [where, "false"])
	count(where) > 0
	where[count(where) - 1] == "checkout"
}

deny contains msg if {
	some [file, doc] in watchers
	not is_object(object.get(doc, "permissions", null))
	msg := sprintf("%s does not spell out its permissions", [file])
}

deny contains msg if {
	some [file, doc] in watchers
	some scope, level in object.get(doc, "permissions", {})
	not level in {"read", "none"}
	scope != "issues"
	msg := sprintf("%s may %s %s, and it runs with the default branch's token whoever started the run it watches", [file, level, scope])
}

deny contains msg if {
	some [file, doc] in watchers
	some name, job in object.get(doc, "jobs", {})
	perms := job.permissions
	some why in watcher_job_problems(perms)
	msg := sprintf("%s, in its job %s, %s", [file, name, why])
}

watcher_job_problems(perms) := {"does not spell out its permissions"} if not is_object(perms)

watcher_job_problems(perms) := {sprintf("may %s %s", [level, scope]) | some scope, level in perms; not level in {"read", "none"}; scope != "issues"} if is_object(perms)
