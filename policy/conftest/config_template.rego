# Every op:// reference in the config template is wrapped in {{ }}.
#
# `op inject` substitutes only the moustache form. A bare op:// reference is
# still valid JSON, still passes every schema check, and is copied through to
# the rendered config verbatim - so the program downstream receives the
# literal reference where it expected an age recipient, a hostname or a
# password.
#
# The Render phase does catch this (AssertRenderedConfigComplete compares the
# template against what came back), but only during a real run, against a real
# vault. This is the same check one tier earlier and with no credentials, so a
# pull request catches it instead of an ignition at 2am. It cost a real bug:
# the estate's backup recipient was added unwrapped when the keypair moved out
# of the per-site item.
package rules.config_template

import rego.v1

template := [f | some f in input; f.path == "config/management.tpl.json"]

wrapped := `\{\{\s*op://[^}]*\}\}`

values contains v if {
	some f in template
	walk(f.contents, [_, v])
	is_string(v)
}

deny contains msg if {
	count(template) == 0
	msg := "config/management.tpl.json was not read, so its vault references were not checked"
}

deny contains msg if {
	some v in values
	contains(regex.replace(v, wrapped, ""), "op://")
	msg := sprintf("config/management.tpl.json has an unwrapped op:// reference (%s). op inject only substitutes {{ op://... }}. Unwrapped, the literal reference string is handed to whatever reads that key.", [v])
}

deny contains msg if {
	count(template) > 0
	n := count([m | some v in values; some m in regex.find_n(wrapped, v, -1)])
	n < 10
	msg := sprintf("only %d wrapped references found; this rule is reading the wrong file", [n])
}
