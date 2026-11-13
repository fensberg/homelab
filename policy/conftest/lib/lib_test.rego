package lib_test

import data.lib
import rego.v1

# What the rules read lib's selectors for is tested with each rule; these
# are the forms of a file no rule's tests happen to hand it.

test_a_yaml_file_is_yaml_whichever_way_its_name_is_spelt if {
	lib.is_yml("a/b.yml")
	lib.is_yml("a/b.yaml")
	not lib.is_yml("a/b.json")
}

test_triggers_are_read_in_every_form_github_takes if {
	lib.triggers({"on": "push"}) == {"push"}
	lib.triggers({"on": ["push", "pull_request"]}) == {"push", "pull_request"}
	lib.triggers({"true": {"schedule": []}}) == {"schedule"}
	lib.triggers({}) == set()
}

test_an_actions_steps_are_read_with_their_environment if {
	action := {"path": ".github/actions/a/action.yml", "contents": {"env": {"A": "1"}, "runs": {"using": "composite", "steps": [{"run": "x", "env": {"B": "2"}}]}}}
	some [file, where, step, envs] in lib.steps_with_env with input as [action]
	file == ".github/actions/a/action.yml"
	where == ""
	step.run == "x"
	envs == [{"A": "1"}, {"B": "2"}]
}

test_a_plans_scope_is_the_directory_the_harness_wrote_it_under if {
	lib.plan_scope("/tmp/x/site/site0-cluster.plan.json") == "site"
	lib.plan_scope("/tmp/x/estate/estate.plan.json") == "estate"
	not lib.plan_scope("/tmp/x/elsewhere/a.plan.json")
	not lib.plan_scope("a.plan.json")
}

test_a_fact_is_known_by_its_name_and_found_nowhere_else if {
	read := lib.facts with input as [
		{"path": "config/facts/a-fact.json", "contents": {"key": "value"}},
		{"path": "config/facts/deeper/b.json", "contents": {}},
		{"path": "config/c.json", "contents": {}},
		{"path": "config/facts/notes.yaml", "contents": {}},
	]
	read == {"a-fact": {"key": "value"}}
}

test_a_plans_module_calls_and_variables_are_read_from_its_root if {
	plan := {"path": "/tmp/x/site/a.plan.json", "contents": {
		"configuration": {"root_module": {
			"module_calls": {"m": {"source": "../tree/parts/m"}},
			"variables": {"tree": {"default": ""}, "unset": {}},
		}},
		"variables": {"tree": {"value": "../tree"}},
	}}
	calls := lib.plan_module_calls with input as [plan, {"path": "a.yaml", "contents": {"kind": "x"}}]
	calls == {["/tmp/x/site/a.plan.json", "m", {"source": "../tree/parts/m"}]}
	vars := lib.plan_variables with input as [plan]
	vars == {
		["/tmp/x/site/a.plan.json", "tree", {"default": ""}, "../tree"],
		["/tmp/x/site/a.plan.json", "unset", {}, null],
	}
}

test_a_plans_outputs_are_read_from_its_root if {
	plan := {"path": "/tmp/x/estate/whole.plan.json", "contents": {"configuration": {"root_module": {"outputs": {"o": {"expression": {"references": ["module.m.use"]}}}}}}}
	outputs := lib.plan_outputs with input as [plan, {"path": "a.yaml", "contents": {"kind": "x"}}]
	outputs == {["/tmp/x/estate/whole.plan.json", "o", {"expression": {"references": ["module.m.use"]}}]}
}

test_what_flux_applies_is_read_as_the_harness_renders_it if {
	layer := {"path": "/tmp/x/layer-0-0.json", "contents": {"layer": {"name": "l", "path": "d"}, "documents": [{"kind": "ConfigMap"}]}}
	lib.rendered_layers == {["/tmp/x/layer-0-0.json", {"name": "l", "path": "d"}, [{"kind": "ConfigMap"}]]} with input as [layer, {"path": "a.yaml", "contents": {"kind": "x"}}]
}

test_a_pinned_plan_is_read_apart_from_the_plans_a_change_makes if {
	resources := {"planned_values": {"root_module": {"resources": [{"address": "a.b", "type": "a", "values": {}}]}}}
	made := {"path": "/tmp/x/site/site0-platform.plan.json", "contents": resources}
	pinned := {"path": "/tmp/x/pinned/site0-platform.plan.json", "contents": {"pinned_plan": object.union(resources, {"configuration": {"root_module": {"outputs": {"o": {}}}}})}}
	count(lib.plan) == 1 with input as [made, pinned]
	{p | some [p, _] in lib.pinned_plan} == {"/tmp/x/pinned/site0-platform.plan.json"} with input as [made, pinned]
	count(lib.plan_outputs) == 0 with input as [pinned]
}

test_the_pools_are_named_from_the_suppliers_list if {
	pools := lib.pull_request_pools with input as [{"path": "scripts/approved-suppliers.yml", "contents": {"pull_request_pools": {"names": ["builds", "source"]}}}]
	pools == {"pr-builds-${{ vars.pull_request_runners_site }}", "pr-source-${{ vars.pull_request_runners_site }}"}
}

test_a_set_of_runners_is_a_pool_by_its_name if {
	suppliers := [{"path": "scripts/approved-suppliers.yml", "contents": {"pull_request_pools": {"names": ["build"]}}}]
	lib.pull_request_pool("pr-build-${SITE}") with input as suppliers
	not lib.pull_request_pool("homelab-${SITE}") with input as suppliers
}

test_a_runner_is_read_as_it_is_written if {
	lib.hosted_runner("ubuntu-24.04")
	not lib.hosted_runner("self-hosted")
	lib.runner_labels("ubuntu-latest") == ["ubuntu-latest"]
	lib.runner_labels(["a", 3]) == ["a", ""]
	lib.runner_labels({"group": "g"}) == []
}

test_an_outside_action_is_named_by_its_repository if {
	sha := "0123456789012345678901234567890123456789"
	refs := [$"actions/fetch@{sha}", $"github/scan/start@{sha}", $"someone/builds@{sha}", "$/.github/workflows/shared", "alone@v1"]
	workflow := {"path": ".github/workflows/w.yml", "contents": {"jobs": {"a": {"steps": [{"uses": r} | some r in refs]}}}}
	composite := {"path": ".github/workflows/shared/action.yml", "contents": {"runs": {"using": "composite", "steps": [{"uses": $"else/where@{sha}"}]}}}
	lib.outside_actions == {"actions/fetch", "github/scan/start", "someone/builds", "else/where"} with input as [workflow, composite]
}

test_what_the_allowed_actions_probe_names_is_not_a_use if {
	sha := "0123456789012345678901234567890123456789"
	workflow := {"path": ".github/workflows/w.yml", "contents": {"jobs": {
		"real": {"steps": [{"uses": $"actions/fetch@{sha}"}]},
		"probe": {"steps": [{"uses": $"else/where@{sha}"}]},
	}}}
	suppliers := {"path": "scripts/approved-suppliers.yml", "contents": {"actions_probe": "w.yml/probe"}}
	lib.actions_probe == [".github/workflows/w.yml", "probe"] with input as [workflow, suppliers]
	lib.outside_actions == {"actions/fetch"} with input as [workflow, suppliers]
	lib.outside_actions == {"actions/fetch", "else/where"} with input as [workflow]
}

test_a_probe_written_in_another_form_is_no_probe if {
	not lib.actions_probe with input as [{"path": "scripts/approved-suppliers.yml", "contents": {"actions_probe": "w.yml"}}]
	not lib.actions_probe with input as [{"path": "scripts/approved-suppliers.yml", "contents": {"actions_probe": ["w.yml/probe"]}}]
}

test_an_action_written_in_another_form_is_named_as_written if {
	workflow := {"path": ".github/workflows/w.yml", "contents": {"jobs": {"a": {"steps": [{"uses": "./.github/workflows/local"}, {"uses": "docker://example.test/image:1"}]}}}}
	lib.outside_actions == {"./.github/workflows/local", "docker://example.test/image:1"} with input as [workflow]
}

test_what_the_pinned_actions_run_is_read_from_what_was_fetched if {
	fetched := {"path": "/tmp/offline/pinned-actions.json", "contents": {"pinned_actions": {"owner/action@abc": ["owner/other@def"]}}}
	lib.what_pinned_actions_run == {"owner/action@abc": ["owner/other@def"]} with input as [fetched, {"path": "plan.json", "contents": {"planned_values": {}}}]
}

test_nothing_fetched_is_nothing_pinned if {
	lib.what_pinned_actions_run == {} with input as [{"path": "plan.json", "contents": {"planned_values": {}}}]
}
