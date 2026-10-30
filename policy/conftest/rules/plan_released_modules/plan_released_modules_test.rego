package rules.plan_released_modules_test

import data.rules.plan_released_modules
import rego.v1

declared := {"release": {"default": ""}, "digest": {"default": ""}, "unreleased": {"default": ""}}

plan(calls, variables, tree) := {"path": "/tmp/plans/site/site0-root.plan.json", "contents": {
	"configuration": {"root_module": {"module_calls": calls, "variables": variables}},
	"variables": {"unreleased": {"value": tree}},
}}

held := plan({"m": {"source": "../../.tree/parts/thing"}}, declared, "../../.tree")

lines := {"path": "management/versions.json", "contents": {"alpha": {"platform": "v1@sha256:x"}}}

template := {"path": "config/management.tpl.json", "contents": {"sites": {"alpha": {}}}}

around := [lines, template]

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_a_site_running_its_release_passes if {
	count(plan_released_modules.deny) == 0 with input as array.concat(around, [held])
}

test_the_estates_plan_is_not_a_sites if {
	estate := object.union(held, {"path": "/tmp/plans/estate/whole.plan.json", "contents": {"configuration": {"root_module": {"module_calls": {"local": {"source": "./runners"}}}}}})
	count(plan_released_modules.deny) == 0 with input as array.concat(around, [held, estate])
}

test_a_module_called_by_path_is_refused if {
	by_path := plan({"m": {"source": "../../parts/thing"}}, declared, "../../.tree")
	denies(plan_released_modules.deny, "and not from the tree it was handed") with input as array.concat(around, [by_path])
}

test_a_default_release_is_refused if {
	defaulted := plan(held.contents.configuration.root_module.module_calls, object.union(declared, {"digest": {"default": "sha256:abc"}}), "../../.tree")
	denies(plan_released_modules.deny, "gives \"digest\" a default of sha256:abc") with input as array.concat(around, [defaulted])
}

test_a_root_not_told_where_its_modules_come_from_is_refused if {
	undeclared := plan(held.contents.configuration.root_module.module_calls, object.remove(declared, ["digest"]), "../../.tree")
	denies(plan_released_modules.deny, "declares no \"digest\" variable") with input as array.concat(around, [undeclared])
}

test_a_root_calling_nothing_is_refused if {
	denies(plan_released_modules.deny, "calls no module") with input as array.concat(around, [plan({}, declared, "../../.tree")])
}

test_a_plan_made_with_no_tree_is_refused if {
	denies(plan_released_modules.deny, "was made with no unreleased tree") with input as array.concat(around, [plan(held.contents.configuration.root_module.module_calls, declared, "")])
}

test_a_site_with_no_line_and_a_line_with_no_site_are_refused if {
	other := {"path": "management/versions.json", "contents": {"gamma": {"platform": "v1@sha256:x"}}}
	denies(plan_released_modules.deny, "alpha is a site, and management/versions.json has no line") with input as [other, template, held]
	denies(plan_released_modules.deny, "gives gamma a release, and the config declares no such site") with input as [other, template, held]
}

test_nothing_to_read_is_refused if {
	denies(plan_released_modules.deny, "no site's plan was read") with input as []
	denies(plan_released_modules.deny, "the config template declares no site") with input as []
}
