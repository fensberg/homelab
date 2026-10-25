package rules.break_glass_identity_test

import data.rules.break_glass_identity
import rego.v1

template := {"path": "config/management.tpl.json", "contents": {"state_backup": {"recipient": "{{ op://estate/state_backup/recipient }}"}}}

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_the_break_glass_identity_in_the_template_is_refused if {
	i := [{"path": "config/management.tpl.json", "contents": {"source_control": {"repo_url": "x"}, "state_backup": {"recipient": "{{ op://estate/state_backup/recipient }}", "identity": "{{ op://estate/state_backup/identity }}"}}}]
	denies(break_glass_identity.deny, `references "state_backup/identity"`) with input as i
}

test_a_template_without_the_recipient_is_refused if {
	denies(break_glass_identity.deny, "no longer references state_backup/recipient") with input as [{"path": "config/management.tpl.json", "contents": {"source_control": {"repo_url": "x"}}}]
}

test_a_template_with_the_recipient_alone_passes if {
	count(break_glass_identity.deny) == 0 with input as [template]
}

test_no_template_is_refused if {
	denies(break_glass_identity.deny, "found no config template") with input as []
}
