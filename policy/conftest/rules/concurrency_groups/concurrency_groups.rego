# A concurrency group names the subject whose newer run replaces it: a pull
# request's number, a comment's issue and the comment itself, and the event
# when a pull request shares a workflow with another (#573, #574).
package rules.concurrency_groups

import data.lib
import rego.v1

groups contains [file, file, doc.concurrency, lib.triggers(doc)] if {
	some file, doc in lib.workflows
	doc.concurrency
}

groups contains [file, $"{file} job {name}", job.concurrency, lib.triggers(doc)] if {
	some file, doc in lib.workflows
	some name, job in object.get(doc, "jobs", {})
	job.concurrency
}

group_of(c) := c if is_string(c)

group_of(c) := c.group if is_object(c)

needs(triggers) := {[ref, why] |
	some [ref, why, when] in [
		["github.event.pull_request.number", "so it is not keyed by the pull request: a merged pull request reports its base branch as github.ref, and its closing run cancels whatever runs there (#573)", pr(triggers)],
		["github.event.issue.number", "so every comment shares one group: a comment event has no pull_request (#574)", "issue_comment" in triggers],
		["github.event.comment", "so a comment that asks for nothing still replaces a run in progress (#574)", "issue_comment" in triggers],
		["github.event_name", "so a pull request's run can share a group with another event's and cancel it (#573)", pr_and_other(triggers)],
	]
	when
}

prs := {"pull_request", "pull_request_target"}

default pr(_) := false

pr(triggers) if count(triggers & prs) > 0

pr_and_other(triggers) if {
	pr(triggers)
	count(triggers - prs) > 0
}

pr_and_other(triggers) := false if not pr(triggers)

pr_and_other(triggers) := false if count(triggers - prs) == 0

deny contains msg if {
	count(groups) == 0
	msg := "no concurrency group was found in any workflow, so the concurrency rule examined nothing"
}

deny contains msg if {
	some [_, where, c, triggers] in groups
	group := group_of(c)
	some [ref, why] in needs(triggers)
	not contains(group, ref)
	msg := $"{where}: the concurrency group \"{group}\" does not use {ref}, {why}"
}
