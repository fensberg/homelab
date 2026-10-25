# Every alert the estate declares says what it is, what it means and the
# first thing to try (the channel shows the step: rules.alert_channel).
#
# An alert that says only that something is wrong is read by somebody who
# then has to work out what to do about it, usually tired and usually from a
# phone. On 2026-10-11 an application was down for five hours between its
# first alert and anybody acting on it. So each alert carries three things of
# its own: what it is (summary), what it means (description), and the first
# thing to try (first_step), kept apart from the rest so that it can be
# shown on a line of its own and so that leaving it out is something a
# machine can see (docs/epochs/04-observability.md, criterion 26).
#
# What this cannot hold is that the step is a good one. That is a person's
# to judge when the alert is written, and when it next fires.
#
# The alerts the monitoring stack brings with it are not the estate's and
# are not held here. They carry a page of their project's, which the
# channel is shown in place of a first step.
package rules.alerts

import data.lib
import rego.v1

declared contains [f.path, r.alert, object.get(r, "annotations", {})] if {
	some f in lib.objects
	f.contents.kind == "PrometheusRule"
	some g in object.get(f.contents.spec, "groups", [])
	some r in object.get(g, "rules", [])
	object.get(r, "alert", "") != ""
}

owed := {"summary": "what it is", "description": "what it means", "first_step": "the first thing to try"}

said(annotations, key) := trim_space(object.get(annotations, key, ""))

unsaid(path, name, annotations) := {msg |
	some key, what in owed
	said(annotations, key) == ""
	msg := $"{path}: the alert {name} does not say {what} ({key})."
} | {msg |
	step := said(annotations, "first_step")
	step != ""
	step in {said(annotations, "summary"), said(annotations, "description")}
	msg := $"{path}: the alert {name} gives as its first step what it has already said. Say what to do."
}

deny contains msg if {
	some [path, name, annotations] in declared
	some problem in unsaid(path, name, annotations)
	msg := $"{problem} Whoever reads an alert is owed what it is, what it means and what to try first, each in an annotation of its own. The channel shows the first step on a line by itself."
}

deny contains msg if {
	count(declared) == 0
	msg := "no alert was found in what Flux applies, so this checked nothing"
}
