package alerts_test

import data.alerts
import rego.v1

rule(rules) := {"path": "f.yaml", "contents": {"kind": "PrometheusRule", "spec": {"groups": [{"rules": rules}]}}}

stack(template) := {"path": "s.yaml", "contents": {"kind": "HelmRelease", "metadata": {"name": "kube-prometheus-stack"}, "spec": {"values": {"alertmanager": {"config": {"receivers": [{"text": template}]}}}}}}

shown := stack("{{ .Annotations.first_step }}")

whole := {"summary": "It is down.", "description": "Nothing answers.", "first_step": "Start it."}

unsaid(annotations) := alerts.unsaid("f.yaml", "A", annotations)

test_the_alerts_of_a_rule_and_of_nothing_else_are_read if {
	i := [rule([{"record": "a:recorded", "expr": "vector(1)"}, {"alert": "Whole", "annotations": whole}]), {"path": "g.yaml", "contents": {"kind": "ConfigMap", "spec": {"groups": [{"rules": [{"alert": "NotARule"}]}]}}}, shown]
	count(alerts.alerts) == 1 with input as i
	count(alerts.deny) == 0 with input as i
}

test_an_alert_saying_all_three_owes_nothing if {
	count(unsaid(whole)) == 0
}

test_an_alert_with_no_step_owes_one if {
	got := unsaid(object.remove(whole, ["first_step"]))
	count(got) == 1
	some msg in got
	contains(msg, "the first thing to try")
}

test_a_step_that_says_it_again_is_refused if {
	got := unsaid(object.union(whole, {"first_step": "It is down."}))
	count(got) == 1
	some msg in got
	contains(msg, "what it has already said")
}

test_a_bare_alert_owes_all_three if {
	count(unsaid({})) == 3
}

test_no_alert_is_refused if {
	some msg in alerts.deny with input as [shown]
	contains(msg, "no alert was found")
}

test_a_channel_not_shown_the_step_is_refused if {
	some msg in alerts.deny with input as [rule([{"alert": "Whole", "annotations": whole}]), stack("{{ .Annotations.summary }}")]
	contains(msg, "does not print an alert's first step")
}
