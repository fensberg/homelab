package rules.plan_restated_facts_test

import data.rules.plan_restated_facts
import rego.v1

fact := {"path": "config/facts/a-fact.json", "contents": {"_comment": ["says 192.0.2.1 and more"], "servers": ["192.0.2.1"], "names": [{"key": "kept", "empty": ""}]}}

plan(expressions) := {"path": "/tmp/plans/site/site0-root.plan.json", "contents": {"configuration": {"root_module": {"resources": [{"address": "thing.this", "mode": "managed", "type": "thing", "expressions": expressions}]}}}}

reads := plan({"servers": {"references": ["local.servers"]}, "comment": {"constant_value": "something else"}})

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_a_fact_read_by_reference_passes if {
	count(plan_restated_facts.deny) == 0 with input as [fact, reads]
}

test_a_fact_written_as_a_constant_is_refused if {
	restated := plan({"servers": {"constant_value": ["192.0.2.1"]}})
	denies(plan_restated_facts.deny, "thing.this writes \"192.0.2.1\" as a constant") with input as [fact, restated]
}

test_a_fact_inside_a_table_written_as_a_constant_is_refused if {
	restated := plan({"block": [{"name": {"constant_value": "kept"}}]})
	denies(plan_restated_facts.deny, "config/facts/a-fact.json's to say") with input as [fact, restated]
}

test_a_comment_or_an_empty_value_is_no_fact if {
	commented := plan({"note": {"constant_value": "says 192.0.2.1 and more"}, "blank": {"constant_value": ""}})
	count(plan_restated_facts.deny) == 0 with input as [fact, commented]
}

test_nothing_to_read_is_refused if {
	denies(plan_restated_facts.deny, "no fact was read") with input as [reads]
	denies(plan_restated_facts.deny, "no plan's configuration writes any constant") with input as [fact]
}
