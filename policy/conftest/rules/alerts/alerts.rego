# Every alert the estate declares says what it means and the first thing to
# try, and the channel is shown both.
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
	msg := sprintf("%s: the alert %s does not say %s (%s).", [path, name, what, key])
} | {msg |
	step := said(annotations, "first_step")
	step != ""
	step in {said(annotations, "summary"), said(annotations, "description")}
	msg := sprintf("%s: the alert %s gives as its first step what it has already said. Say what to do.", [path, name])
}

deny contains msg if {
	some [path, name, annotations] in declared
	some problem in unsaid(path, name, annotations)
	msg := concat(" ", [problem, "Whoever reads an alert is owed what it is, what it means and what to try first, each in an annotation of its own. The channel shows the first step on a line by itself."])
}

deny contains msg if {
	count(declared) == 0
	msg := "no alert was found in what Flux applies, so this checked nothing"
}

stack := [f | some f in lib.objects; f.contents.kind == "HelmRelease"; f.contents.metadata.name == "kube-prometheus-stack"]

shows_the_first_step(f) if {
	walk(f.contents, [_, v])
	is_string(v)
	contains(v, ".Annotations.first_step")
}

deny contains msg if {
	count(stack) != 1
	msg := sprintf("%d HelmRelease named kube-prometheus-stack were found, so what the channel is shown cannot be read", [count(stack)])
}

deny contains msg if {
	some f in stack
	not shows_the_first_step(f)
	msg := sprintf("%s: what Alertmanager sends the channel does not print an alert's first step, so every alert's is written and none is read.", [f.path])
}
