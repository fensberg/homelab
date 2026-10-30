# What the channel is sent of an alert shows its first step on a line of its
# own: each alert carries one (rules.alerts), and a step written and not
# shown is a step nobody reads. The monitoring stack is read for the template
# Alertmanager sends the channel; there is one of it, or what the channel is
# shown cannot be read.
package rules.alert_channel

import data.lib
import rego.v1

stack := [f | some f in lib.objects; f.contents.kind == "HelmRelease"; f.contents.metadata.name == "kube-prometheus-stack"]

shows_the_first_step(f) if {
	walk(f.contents, [_, v])
	is_string(v)
	contains(v, ".Annotations.first_step")
}

deny contains msg if {
	count(stack) != 1
	msg := $"{count(stack)} HelmRelease named kube-prometheus-stack were found, so what the channel is shown cannot be read"
}

deny contains msg if {
	some f in stack
	not shows_the_first_step(f)
	msg := $"{f.path}: what Alertmanager sends the channel does not print an alert's first step, so every alert's is written and none is read."
}
