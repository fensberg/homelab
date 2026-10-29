package rules.runner_namespaces_test

import data.rules.runner_namespaces
import rego.v1

fact := {"path": "config/facts/runner-namespaces.json", "contents": {"system": "sys", "runners": "own", "pull_request_runners": "prs"}}

suppliers := {"path": "scripts/approved-suppliers.yml", "contents": {"pull_request_pools": {"names": ["build"]}}}

release(name, namespace, chart, set) := {"path": $"clusters/core/{name}.yaml", "contents": {
	"kind": "HelmRelease",
	"metadata": {"name": name, "namespace": namespace},
	"spec": {"chart": {"spec": {"chart": chart}}, "values": {"runnerScaleSetName": set}},
}}

controller(namespace) := release("controller", namespace, "gha-runner-scale-set-controller", "")

estates(namespace) := release("estates", namespace, "gha-runner-scale-set", "homelab-${SITE}")

pool(namespace) := release("pool", namespace, "gha-runner-scale-set", "pr-build-${SITE}")

policy(namespace, set) := {"path": "clusters/core/policy.yaml", "contents": {
	"kind": "CiliumNetworkPolicy",
	"metadata": {"name": "walls", "namespace": namespace},
	"spec": {"endpointSelector": {"matchLabels": {"actions.github.com/scale-set-name": set}}},
}}

sweeper(told) := {"path": "clusters/core/sweeper.yaml", "contents": {
	"kind": "Deployment",
	"metadata": {"name": "sweeper", "namespace": "sweeping"},
	"spec": {"template": {"spec": {"serviceAccountName": "sweeper", "containers": [{"name": "sweeper", "args": ["-runners", told]}]}}},
}}

binding(namespace) := {"path": "clusters/core/sweeper.yaml", "contents": {
	"kind": "RoleBinding",
	"metadata": {"name": "sweeper", "namespace": namespace},
	"subjects": [{"kind": "ServiceAccount", "name": "sweeper", "namespace": "sweeping"}],
}}

around := [policy("prs", "pr-build-${SITE}"), sweeper("prs"), binding("prs")]

good := array.concat([fact, suppliers, controller("sys"), estates("own"), pool("prs")], around)

with_around(objects) := array.concat([fact, suppliers, controller("sys"), estates("own"), pool("prs")], objects)

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_each_release_in_its_namespace_passes if {
	count(runner_namespaces.deny) == 0 with input as good
}

test_the_controller_elsewhere_is_refused if {
	denies(runner_namespaces.deny, "controller is the runners' controller, and is in the namespace \"own\"") with input as array.concat([fact, suppliers, controller("own"), estates("own"), pool("prs")], around)
}

test_a_pool_among_the_estates_runners_is_refused if {
	denies(runner_namespaces.deny, "pool is a pull-request pool, and is in the namespace \"own\"") with input as array.concat([fact, suppliers, controller("sys"), estates("own"), pool("own")], around)
}

test_the_estates_runners_with_no_namespace_are_refused if {
	unnamed := object.union(estates("own"), {"contents": {"metadata": {"name": "estates", "namespace": null}}})
	denies(runner_namespaces.deny, "estates is a set of the estate's own runners, and is in the namespace") with input as array.concat([fact, suppliers, controller("sys"), unnamed, pool("prs")], around)
}

test_a_fact_missing_a_namespace_is_refused if {
	partial := {"path": "config/facts/runner-namespaces.json", "contents": {"system": "sys", "runners": "own"}}
	denies(runner_namespaces.deny, "gives no pull_request_runners namespace") with input as array.concat([partial, suppliers, controller("sys"), estates("own"), pool("prs")], around)
}

test_a_policy_in_another_namespace_than_its_runners_is_refused if {
	denies(runner_namespaces.deny, "selects the runners of pr-build-${SITE}, which are in \"prs\", from \"own\"") with input as with_around([policy("own", "pr-build-${SITE}"), sweeper("prs"), binding("prs")])
}

test_a_policy_for_runners_no_release_makes_is_refused if {
	denies(runner_namespaces.deny, "no release makes a set of that name") with input as with_around([policy("prs", "pr-gone-${SITE}"), sweeper("prs"), binding("prs")])
}

test_the_sweeper_told_another_namespace_is_refused if {
	denies(runner_namespaces.deny, "is told the pull-request runners are in \"own\"") with input as with_around([policy("prs", "pr-build-${SITE}"), sweeper("own"), binding("own")])
}

test_the_sweeper_granted_nothing_where_it_watches_is_refused if {
	denies(runner_namespaces.deny, "nothing grants sweeper anything there") with input as with_around([policy("prs", "pr-build-${SITE}"), sweeper("prs"), binding("own")])
}

test_nothing_to_read_is_refused if {
	denies(runner_namespaces.deny, "nothing selects a set of runners") with input as [fact]
	denies(runner_namespaces.deny, "nothing is told where the pull-request runners are") with input as [fact]
	denies(runner_namespaces.deny, "no HelmRelease installs the runners' controller") with input as [fact]
	denies(runner_namespaces.deny, "no set of the estate's own runners was read") with input as [fact]
	denies(runner_namespaces.deny, "no pull-request pool was read") with input as [fact]
	denies(runner_namespaces.deny, "gives no system namespace") with input as []
}
