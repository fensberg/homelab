package rules.alert_channel_test

import data.rules.alert_channel
import rego.v1

stack(template) := {"path": "s.yaml", "contents": {"kind": "HelmRelease", "metadata": {"name": "kube-prometheus-stack"}, "spec": {"values": {"alertmanager": {"config": {"receivers": [{"text": template}]}}}}}}

shown := stack("{{ .Annotations.first_step }}")

test_a_channel_not_shown_the_step_is_refused if {
	some msg in alert_channel.deny with input as [stack("{{ .Annotations.summary }}")]
	contains(msg, "does not print an alert's first step")
}

test_a_channel_shown_the_step_passes if {
	count(alert_channel.deny) == 0 with input as [shown]
}

test_no_stack_is_refused if {
	some msg in alert_channel.deny with input as []
	contains(msg, "0 HelmRelease named kube-prometheus-stack")
}
