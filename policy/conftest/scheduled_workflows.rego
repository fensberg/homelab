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
# And the asking fits what GitHub allows a caller with no token: sixty
# requests an hour from one address. More workflows or a shorter interval
# than that allows is refused here, not found out when GitHub starts
# refusing every question.
#
# Every scheduled workflow also has a deadline, and it is the deadline for
# how often that workflow is asked for. A workflow that stops running says
# nothing: its last run stays the newest one, and the alert for a failing
# schedule is content with it. The deadlines differ by how often a workflow
# is asked for, and a workflow named under the wrong one is either never
# late or always late. One that is named under none has no deadline at all,
# which is how it was for all of them.
package scheduled_workflows

import data.lib
import rego.v1

name := "scheduled-workflows"

basename(path) := parts[count(parts) - 1] if parts := split(path, "/")

on_of(doc) := object.get(doc, "on", object.get(doc, "true", {}))

scheduled contains basename(file) if {
	some file, doc in lib.workflows
	"schedule" in lib.triggers(doc)
}

object_of(kind) := [f | some f in input; f.contents.kind == kind; f.contents.metadata.name == name]

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
	msg := sprintf("%d ScrapeConfig named %s were found, and the watch is one", [count(scrape), name])
}

deny contains msg if {
	some t in targets
	not regex.match(workflow_of, t)
	msg := sprintf("%s asks %q, which is not a workflow's latest completed scheduled run. Any other question gets an answer the rule does not read.", [where(scrape), t])
}

deny contains msg if {
	some file in scheduled
	not file in asked
	msg := sprintf("%s runs on a schedule and the cluster is not asked about it, so its failures reach nobody (#441). Add its address to the targets in %s.", [file, where(scrape)])
}

deny contains msg if {
	some file in asked
	not file in scheduled
	msg := sprintf("%s asks about %s, and no workflow of that name runs on a schedule. A renamed workflow drops out of the watch without a word; name it as it is now.", [where(scrape), file])
}

interval := object.get(scrape[0].contents.spec, "scrapeInterval", "")

deny contains msg if {
	count(scrape) == 1
	not regex.match(`^\d+m$`, interval)
	msg := sprintf("%s asks every %q, which is not a number of minutes, so what it costs GitHub's allowance cannot be worked out", [where(scrape), interval])
}

allowed := 60

share := 2

deny contains msg if {
	regex.match(`^\d+m$`, interval)
	every_minutes := to_number(trim_suffix(interval, "m"))
	an_hour := floor((count(asked) * 60) / every_minutes)
	an_hour > allowed / share
	msg := sprintf("%s asks GitHub %d times an hour: %d workflows every %d minutes. A caller with no token is allowed %d from one address, and this may use half. Ask less often.", [where(scrape), an_hour, count(asked), every_minutes, allowed])
}

# How often each scheduled workflow is asked for: the most often of its
# crons.

rank := {"hourly": 0, "daily": 1, "weekly": 2}

often(fields) := "weekly" if fields[4] != "*"

often(fields) := "daily" if {
	fields[4] == "*"
	fields[1] != "*"
}

often(fields) := "hourly" if {
	fields[4] == "*"
	fields[1] == "*"
}

crons contains [basename(file), s.cron] if {
	some file, doc in lib.workflows
	some s in object.get(on_of(doc), "schedule", [])
}

deny contains msg if {
	some [file, cron] in crons
	count(split(trim_space(cron), " ")) != 5
	msg := sprintf("%s is scheduled by %q, which is not five fields", [file, cron])
}

asked_for[file] := least if {
	some [file, _] in crons
	ranks := {rank[often(f)] | some [n, c] in crons; n == file; f := split(trim_space(c), " "); count(f) == 5}
	least := [o | some o, r in rank; r == min(ranks)][0]
}

deny contains msg if {
	count(crons) == 0
	msg := "no workflow was found with a schedule, so this checked nothing"
}

alert := "ScheduledWorkflowHasNotRunWhenDue"

alerts contains r if {
	some f in object_of("PrometheusRule")
	some g in f.contents.spec.groups
	some r in g.rules
	object.get(r, "alert", "") == alert
}

named := `instance=~"([^"]+)"`

deny contains msg if {
	some r in alerts
	count(regex.find_all_string_submatch_n(named, r.expr, -1)) == 0
	msg := sprintf("an alert %s in %s names no workflow, so it is about every one of them at one deadline", [alert, where(object_of("PrometheusRule"))])
}

deadlines contains [file, object.get(r.labels, "asked_for", "")] if {
	some r in alerts
	some m in regex.find_all_string_submatch_n(named, r.expr, -1)
	some file in split(m[1], "|")
}

deny contains msg if {
	some [file, _] in deadlines
	given := sort({g | some [f, g] in deadlines; f == file})
	count(given) > 1
	msg := sprintf("%s is given %d deadlines in %s, for %s", [file, count(given), where(object_of("PrometheusRule")), concat(" and for ", given)])
}

deny contains msg if {
	some file, wanted in asked_for
	not file in {f | some [f, _] in deadlines}
	msg := sprintf("%s is asked for %s and has no deadline: if it stopped running, nothing would say so. Name it in the %s alert for %s workflows in %s.", [file, wanted, alert, wanted, where(object_of("PrometheusRule"))])
}

deny contains msg if {
	some [file, got] in deadlines
	wanted := asked_for[file]
	got != wanted
	msg := sprintf("%s is asked for %s and is held to the deadline for a %s workflow in %s, so it is either never late or always late.", [file, wanted, got, where(object_of("PrometheusRule"))])
}

deny contains msg if {
	some [file, _] in deadlines
	not asked_for[file]
	msg := sprintf("%s gives %s a deadline, and no workflow of that name runs on a schedule.", [where(object_of("PrometheusRule")), file])
}
