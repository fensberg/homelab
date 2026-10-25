package rules.alerts_test

import data.rules.alerts
import rego.v1

rule(rules) := {"path": "f.yaml", "contents": {"kind": "PrometheusRule", "spec": {"groups": [{"rules": rules}]}}}

whole := {"summary": "It is down.", "description": "Nothing answers.", "first_step": "Start it."}

unsaid(annotations) := alerts.unsaid("f.yaml", "A", annotations)

test_the_alerts_of_a_rule_and_of_nothing_else_are_read if {
	i := [rule([{"record": "a:recorded", "expr": "vector(1)"}, {"alert": "Whole", "annotations": whole}]), {"path": "g.yaml", "contents": {"kind": "ConfigMap", "spec": {"groups": [{"rules": [{"alert": "NotARule"}]}]}}}]
	count(alerts.declared) == 1 with input as i
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
	some msg in alerts.deny with input as []
	contains(msg, "no alert was found")
}
