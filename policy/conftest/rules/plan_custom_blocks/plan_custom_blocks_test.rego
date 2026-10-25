package rules.plan_custom_blocks_test

import data.rules.plan_custom_blocks
import rego.v1

configured(path, root, calls) := {"path": path, "contents": {"configuration": {"root_module": {"resources": root, "module_calls": calls}}}}

resource(address, mode) := {"address": address, "mode": mode, "type": "x_thing", "expressions": {}}

roots := [
	configured("alpha.plan.json", [resource("x_thing.here", "managed"), resource("data.x_thing.read", "data")], {"part": {"module": {"resources": [resource("x_thing.in_a_module_beside_it", "managed")]}}}),
	configured("beta.plan.json", [], {"shared": {"module": {"resources": [resource("x_thing.in_a_module", "managed")]}}}),
	configured("gamma.plan.json", [], {"other": {"module": {"resources": [resource("x_thing.elsewhere", "managed")]}}}),
]

listing(entries) := {"path": "tests/custom-blocks.yml", "contents": {"opentofu": entries, "go": []}}

test_only_what_a_root_itself_declares_is_found if {
	plan_custom_blocks.found == {"x_thing.here"} with input as roots
}

test_a_declared_custom_block_passes if {
	count(plan_custom_blocks.deny) == 0 with input as array.concat(roots, [listing([{"resource": "x_thing.here", "reason": "There is one of it."}])])
}

test_an_undeclared_one_is_refused if {
	some msg in plan_custom_blocks.deny with input as array.concat(roots, [listing([])])
	contains(msg, `"x_thing.here" is declared in a root rather than in a module`)
}

test_a_declaration_with_no_reason_is_refused if {
	some msg in plan_custom_blocks.deny with input as array.concat(roots, [listing([{"resource": "x_thing.here", "reason": " "}])])
	contains(msg, "with no reason")
}

test_a_declaration_for_nothing_is_refused if {
	entries := [{"resource": "x_thing.here", "reason": "r"}, {"resource": "x_thing.gone", "reason": "r"}]
	some msg in plan_custom_blocks.deny with input as array.concat(roots, [listing(entries)])
	contains(msg, `declares "x_thing.gone", and no such OpenTofu resource exists any more`)
}

test_too_few_roots_is_refused if {
	some msg in plan_custom_blocks.deny with input as [roots[0], listing([])]
	contains(msg, "found 1 plan(s) of a root")
}
