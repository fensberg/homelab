# The cluster is asked about every workflow that runs on a schedule, and
# about nothing else.
#
# A scheduled failure notifies only whoever last edited the cron, by email
# (#441). The cluster asks GitHub for each scheduled workflow's latest run
# and raises an alert while it is one that failed. It is asked by address,
# one for each workflow's file, so both directions are held: a scheduled
# workflow it is not asked about is one whose failures reach nobody, and an
# address for a file that has no schedule is a rename that quietly stopped
# the watch, still spending the hourly allowance on a question with no
# answer.
#
# That the asking fits GitHub's allowance is rules.scheduled_allowance, and
# that each workflow has its deadline is rules.scheduled_deadlines.
package rules.scheduled_workflows

import data.lib
import rego.v1

name := "scheduled-workflows"

basename(path) := parts[count(parts) - 1] if parts := split(path, "/")

scheduled contains basename(file) if {
	some file, doc in lib.workflows
	"schedule" in lib.triggers(doc)
}

object_of(kind) := [f | some f in lib.objects; f.contents.kind == kind; f.contents.metadata.name == name]

scrape := object_of("ScrapeConfig")

where(objects) := concat(", ", sort({f.path | some f in objects}))

targets contains t if {
	some f in scrape
	some sc in object.get(f.contents.spec, "staticConfigs", [])
	some t in object.get(sc, "targets", [])
}

workflow_of := `/actions/workflows/([^/]+)/runs\?event=schedule&status=completed&per_page=1$`

asked contains m[1] if {
	some t in targets
	some m in regex.find_all_string_submatch_n(workflow_of, t, 1)
}

deny contains msg if {
	count(scheduled) == 0
	msg := "no workflow runs on a schedule, so this checked nothing: the reader has stopped matching"
}

deny contains msg if {
	count(scrape) != 1
	msg := $"{count(scrape)} ScrapeConfig named {name} were found, and the watch is one"
}

deny contains msg if {
	some t in targets
	not regex.match(workflow_of, t)
	msg := $"{where(scrape)} asks \"{t}\", which is not a workflow's latest completed scheduled run. Any other question gets an answer the rule does not read."
}

deny contains msg if {
	some file in scheduled
	not file in asked
	msg := $"{file} runs on a schedule and the cluster is not asked about it, so its failures reach nobody (#441). Add its address to the targets in {where(scrape)}."
}

deny contains msg if {
	some file in asked
	not file in scheduled
	msg := $"{where(scrape)} asks about {file}, and no workflow of that name runs on a schedule. A renamed workflow drops out of the watch without a word; name it as it is now."
}
