package custom_blocks_test

import data.custom_blocks
import rego.v1

tf(path, contents) := {"path": path, "contents": contents}

roots := [
	tf("ground/alpha/access.tf", {"provider": {"x": [{}]}, "resource": {"x_thing": {"here": [{}]}}, "data": {"x_thing": {"read": [{}]}}}),
	tf("ground/alpha/part/inside.tf", {"resource": {"x_thing": {"in_a_module_beside_it": [{}]}}}),
	tf("parts/shared/a.tf", {"resource": {"x_thing": {"in_a_module": [{}]}}}),
	tf("ground/beta/access.tf", {"provider": {"x": [{}]}}),
	tf("ground/gamma/access.tf", {"provider": {"x": [{}]}}),
	tf("ground/alpha/tests/t.tf", {"provider": {"x": [{}]}, "resource": {"x_thing": {"fixture": [{}]}}}),
]

listing(entries) := {"path": "tests/custom-blocks.yml", "contents": {"opentofu": entries, "go": []}}

test_only_what_a_root_itself_declares_is_found if {
	custom_blocks.found == {"x_thing.here"} with input as roots
}

test_a_declared_custom_block_passes if {
	count(custom_blocks.deny) == 0 with input as array.concat(roots, [listing([{"resource": "x_thing.here", "reason": "There is one of it."}])])
}

test_an_undeclared_one_is_refused if {
	some msg in custom_blocks.deny with input as array.concat(roots, [listing([])])
	contains(msg, `"x_thing.here" is declared in a root rather than in a module`)
}

test_a_declaration_with_no_reason_is_refused if {
	some msg in custom_blocks.deny with input as array.concat(roots, [listing([{"resource": "x_thing.here", "reason": " "}])])
	contains(msg, "with no reason")
}

test_a_declaration_for_nothing_is_refused if {
	entries := [{"resource": "x_thing.here", "reason": "r"}, {"resource": "x_thing.gone", "reason": "r"}]
	some msg in custom_blocks.deny with input as array.concat(roots, [listing(entries)])
	contains(msg, `declares "x_thing.gone", and no such OpenTofu resource exists any more`)
}

test_too_few_roots_is_refused if {
	some msg in custom_blocks.deny with input as [roots[0], listing([])]
	contains(msg, "found 1 OpenTofu root(s)")
}
