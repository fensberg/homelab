package rules.config_template_test

import data.rules.config_template
import rego.v1

# A brace of a template a message quotes, which a template string cannot
# hold as it is.
open := "{"

close := "}"

refs[$"k{i}"] := $"{open}{open} op://estate/item/field{i} {close}{close}" if {
	some i in numbers.range(1, 10)
}

template(extra) := [{"path": "config/management.tpl.json", "contents": object.union(refs, extra)}]

test_wrapped_references_pass if {
	count(config_template.deny) == 0 with input as template({})
}

test_a_bare_reference_is_refused if {
	some msg in config_template.deny with input as template({"site": {"recipient": "op://estate/state_backup/recipient"}})
	contains(msg, "unwrapped op:// reference (op://estate/state_backup/recipient)")
}

test_one_wrapped_beside_one_bare_is_refused if {
	some msg in config_template.deny with input as template({"pair": "{{ op://a/b/c }} and op://a/b/d"})
	contains(msg, "unwrapped op:// reference")
}

test_too_few_references_is_refused if {
	some msg in config_template.deny with input as [{"path": "config/management.tpl.json", "contents": {"k": "{{ op://a/b/c }}"}}]
	contains(msg, "only 1 wrapped references found")
}

test_no_template_is_refused if {
	some msg in config_template.deny with input as []
	contains(msg, "config/management.tpl.json was not read")
}
