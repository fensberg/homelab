package rules.plan_runner_group_test

import data.rules.plan_runner_group
import rego.v1

group(address, values) := {"address": address, "mode": "managed", "type": "github_actions_runner_group", "values": values}

walled(workflows) := group("module.runners.github_actions_runner_group.this", {
	"name": "estate",
	"visibility": "selected",
	"restricted_to_workflows": true,
	"selected_workflows": [$"owner/repo/.github/workflows/{w}@refs/heads/main" | some w in workflows],
})

open_group := group("module.pools.github_actions_runner_group.this", {
	"name": "pools",
	"visibility": "selected",
	"restricted_to_workflows": false,
	"selected_workflows": null,
})

plan(resources) := {"path": "/tmp/plans/estate/estate.plan.json", "contents": {"planned_values": {"root_module": {"resources": resources}}}}

workflow(name, runs_on) := {"path": $".github/workflows/{name}", "contents": {"jobs": {"only": {"runs-on": runs_on}}}}

runners(name, group_name, set) := {"path": $"clusters/core/{name}.yaml", "contents": {
	"kind": "HelmRelease",
	"spec": {
		"chart": {"spec": {"chart": "gha-runner-scale-set"}},
		"values": {"runnerGroup": group_name, "runnerScaleSetName": set},
	},
}}

suppliers := {"path": "scripts/approved-suppliers.yml", "contents": {"pull_request_pools": {"names": ["build"]}}}

pool_label := "pr-build-${{ vars.pull_request_runners_site }}"

hosted := [workflow($"hosted-{i}.yml", "ubuntu-24.04") | some i in numbers.range(1, 4)]

good := array.concat(hosted, [
	plan([walled(["converge.yml"]), open_group]),
	workflow("converge.yml", ["self-hosted", "estate"]),
	workflow("checks.yml", pool_label),
	runners("estate", "estate", "estate-${SITE}"),
	runners("pool", "pools", "pr-build-${SITE}"),
	suppliers,
])

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_each_workflow_admitted_where_it_asks_passes if {
	count(plan_runner_group.deny) == 0 with input as good
}

test_a_workflow_asking_and_not_admitted_is_refused if {
	moved := array.concat(good, [workflow("moved.yml", "estate")])
	denies(plan_runner_group.deny, "moved.yml asks for the estate's runner and no group admits it") with input as moved
}

test_an_expression_or_a_group_asks_for_the_estates_own if {
	moved := array.concat(good, [workflow("grouped.yml", {"group": "estate"})])
	denies(plan_runner_group.deny, "grouped.yml asks for the estate's runner") with input as moved
}

test_a_workflow_admitted_and_not_asking_is_refused if {
	stale := array.concat(hosted, [
		plan([walled(["converge.yml", "gone.yml"]), open_group]),
		workflow("converge.yml", "estate"),
		workflow("checks.yml", pool_label),
		runners("estate", "estate", "estate-${SITE}"),
		runners("pool", "pools", "pr-build-${SITE}"),
		suppliers,
	])
	denies(plan_runner_group.deny, "gone.yml is admitted to the estate's runners and no job of it asks") with input as stale
}

test_a_group_admitting_unchosen_repositories_is_refused if {
	everyone := group("module.runners.github_actions_runner_group.this", object.union(walled(["converge.yml"]).values, {"visibility": "all"}))
	denies(plan_runner_group.deny, "admits repositories nobody chose") with input as array.concat(good, [plan([everyone])])
}

test_a_group_not_saying_whether_it_is_walled_is_refused if {
	unsaid := group("module.other.github_actions_runner_group.this", {"name": "other", "visibility": "selected"})
	denies(plan_runner_group.deny, "does not say whether it admits any workflow") with input as array.concat(good, [plan([unsaid])])
}

test_a_walled_group_naming_no_workflow_is_refused if {
	denies(plan_runner_group.deny, "names no workflow it admits") with input as array.concat(good, [plan([walled([])])])
}

test_an_open_group_nothing_joins_is_refused if {
	idle := array.concat(hosted, [
		plan([walled(["converge.yml"]), open_group]),
		workflow("converge.yml", "estate"),
		runners("estate", "estate", "estate-${SITE}"),
		suppliers,
	])
	denies(plan_runner_group.deny, "the group pools takes any workflow and no set of runners joins it") with input as idle
}

test_the_estates_own_runners_in_an_open_group_are_refused if {
	joined := array.concat(good, [runners("strays", "pools", "estate-batch-${SITE}")])
	denies(plan_runner_group.deny, "a set of the estate's own runners joins it") with input as joined
}

test_a_pool_in_a_walled_group_is_refused if {
	joined := array.concat(good, [runners("walled-pool", "estate", "pr-build-${SITE}")])
	denies(plan_runner_group.deny, "a pull-request pool joins the group estate") with input as joined
}

test_a_pool_asked_for_with_no_open_group_is_refused if {
	shut := array.concat(hosted, [
		plan([walled(["converge.yml"])]),
		workflow("converge.yml", "estate"),
		workflow("checks.yml", pool_label),
		runners("estate", "estate", "estate-${SITE}"),
		suppliers,
	])
	denies(plan_runner_group.deny, "no group is declared for the pools to join") with input as shut
}

test_a_label_not_a_string_is_the_estates_own if {
	odd := array.concat(good, [workflow("odd.yml", [1])])
	denies(plan_runner_group.deny, "odd.yml asks for the estate's runner") with input as odd
}

test_nothing_to_read_is_refused if {
	denies(plan_runner_group.deny, "no group of runners is in the estate's plan") with input as []
	denies(plan_runner_group.deny, "no workflow asks for the estate's runner") with input as []
	denies(plan_runner_group.deny, "only 0 workflow(s) were read") with input as []
}
