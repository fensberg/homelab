# Every scheduled workflow has a deadline, and it is the deadline for how
# often that workflow is asked for. A workflow that stops running says
# nothing: its last run stays the newest one, and the alert for a failing
# schedule is content with it. The deadlines differ by how often a workflow
# is asked for, and a workflow named under the wrong one is either never late
# or always late. One that is named under none has no deadline at all, which
# is how it was for all of them. That the cluster is asked about every
# scheduled workflow is rules.scheduled_workflows.
package rules.scheduled_deadlines

import data.lib
import rego.v1

name := "scheduled-workflows"

basename(path) := parts[count(parts) - 1] if parts := split(path, "/")

on_of(doc) := object.get(doc, "on", object.get(doc, "true", {}))

object_of(kind) := [f | some f in lib.objects; f.contents.kind == kind; f.contents.metadata.name == name]

where(objects) := concat(", ", sort({f.path | some f in objects}))

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

asked_for[file] := least if {
	some [file, _] in crons
	ranks := {rank[often(f)] | some [n, c] in crons; n == file; f := split(trim_space(c), " "); count(f) == 5}
	least := [o | some o, r in rank; r == min(ranks)][0]
}

alert := "ScheduledWorkflowHasNotRunWhenDue"

alerts contains r if {
	some f in object_of("PrometheusRule")
	some g in f.contents.spec.groups
	some r in g.rules
	r.alert == alert
}

named := `instance=~"([^"]+)"`

deadlines contains [file, object.get(r.labels, "asked_for", "")] if {
	some r in alerts
	some m in regex.find_all_string_submatch_n(named, r.expr, -1)
	some file in split(m[1], "|")
}

deny contains msg if {
	some [file, cron] in crons
	count(split(trim_space(cron), " ")) != 5
	msg := $"{file} is scheduled by \"{cron}\", which is not five fields"
}

deny contains msg if {
	count(crons) == 0
	msg := "no workflow was found with a schedule, so this checked nothing"
}

deny contains msg if {
	msg := $"an alert {alert} in {where(object_of("PrometheusRule"))} names no workflow, so it is about every one of them at one deadline"
	some r in alerts
	count(regex.find_all_string_submatch_n(named, r.expr, -1)) == 0
}

deny contains msg if {
	some [file, _] in deadlines
	given := sort({g | some [f, g] in deadlines; f == file})
	count(given) > 1
	msg := $"{file} is given {count(given)} deadlines in {where(object_of("PrometheusRule"))}, for {concat(" and for ", given)}"
}

deny contains msg if {
	some file, wanted in asked_for
	not file in {f | some [f, _] in deadlines}
	msg := $"{file} is asked for {wanted} and has no deadline: if it stopped running, nothing would say so. Name it in the {alert} alert for {wanted} workflows in {where(object_of("PrometheusRule"))}."
}

deny contains msg if {
	some [file, got] in deadlines
	wanted := asked_for[file]
	got != wanted
	msg := $"{file} is asked for {wanted} and is held to the deadline for a {got} workflow in {where(object_of("PrometheusRule"))}, so it is either never late or always late."
}

deny contains msg if {
	some [file, _] in deadlines
	not asked_for[file]
	msg := $"{where(object_of("PrometheusRule"))} gives {file} a deadline, and no workflow of that name runs on a schedule."
}
