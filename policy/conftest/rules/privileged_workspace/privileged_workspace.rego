# A run on a trigger anybody's pull request reaches, holding secrets, never
# puts the pull request's code in the workspace, where every later step and
# every ./ action executes from.
package rules.privileged_workspace

import data.lib
import rego.v1

privileged contains [file, doc] if {
	some file, doc in lib.workflows
	count(lib.triggers(doc) & {"issue_comment", "pull_request_target", "workflow_run"}) > 0
}

moves_workspace := `\bgit\b[^\n;&|]*\s(checkout|switch|reset|restore|pull|merge|read-tree|stash\s+apply)\b`

deny contains msg if {
	count(privileged) == 0
	msg := "no workflow runs on a privileged trigger, and clerk.yml does - the reading has stopped matching"
}

deny contains msg if {
	some [file, doc] in privileged
	some name, job in object.get(doc, "jobs", {})
	some step in object.get(job, "steps", [])
	run := object.get(step, "run", "")
	m := regex.find_n(moves_workspace, run, 1)[0]
	msg := $"{file} job \"{name}\" step \"{object.get(step, "name", "")}\" runs \"{trim_space(m)}\". This workflow runs with secrets on a trigger that anybody's pull request can reach, and that command replaces what is in the workspace - where every later step, and every ./ action, executes from. A pull request's code belongs in its own worktree, read and never run."
}

deny contains msg if {
	some [file, doc] in privileged
	some name, job in object.get(doc, "jobs", {})
	some step in object.get(job, "steps", [])
	run := object.get(step, "run", "")
	regex.match(`refs/pull/|pull_request\.head`, run)
	not contains(run, "git worktree add")
	msg := $"{file} job \"{name}\" step \"{object.get(step, "name", "")}\" fetches a pull request's code without its own worktree. In a run holding secrets, a pull request is data. Fetch it into a worktree of its own - `git worktree add --detach <dir> <commit>` - so nothing that executes from the workspace can be reached by it."
}
