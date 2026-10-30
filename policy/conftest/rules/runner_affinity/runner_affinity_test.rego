package rules.runner_affinity_test

import data.rules.runner_affinity
import rego.v1

keep_off := [{"matchExpressions": [{"key": "node-role.kubernetes.io/control-plane", "operator": "DoesNotExist"}]}]

set(name, scale_set, terms, requests) := {"path": $"clusters/core/{name}.yaml", "contents": {"kind": "HelmRelease", "metadata": {"name": name, "namespace": "arc-runners"}, "spec": {"chart": {"spec": {"chart": "gha-runner-scale-set"}}, "values": {"runnerScaleSetName": scale_set, "template": {"spec": {"affinity": {"nodeAffinity": {"requiredDuringSchedulingIgnoredDuringExecution": {"nodeSelectorTerms": terms}}}, "containers": [{"name": "runner", "resources": {"requests": requests}}]}}}}}}

sized := {"cpu": "1", "memory": "2Gi"}

controller := {"path": "clusters/core/controller.yaml", "contents": {"kind": "HelmRelease", "metadata": {"name": "gha-runner-scale-set-controller", "namespace": "arc-systems"}}}

locals := {"path": "modules/p/variables.tf", "contents": {"locals": [{"runner_system_namespace": "arc-systems", "runners_namespace": "arc-runners"}]}}

workflow(labels) := {"path": ".github/workflows/w.yml", "contents": {"jobs": {$"j{i}": {"runs-on": l} | some i, l in labels}}}

good := [
	controller,
	locals,
	set("self-hosted", "homelab-${SITE}", keep_off, sized),
	set("batch", "homelab-batch-${SITE}", keep_off, sized),
	workflow(["homelab-${{ matrix.site }}", "homelab-batch-${{ matrix.site }}", "ubuntu-latest"]),
]

swap(i, f) := array.flatten([array.slice(good, 0, i), [f], array.slice(good, i + 1, count(good))])

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_the_runners_as_they_should_be_pass if {
	count(runner_affinity.deny) == 0 with input as good
}

test_a_runner_admitted_to_a_control_plane_is_refused if {
	denies(runner_affinity.deny, "would schedule onto a control-plane node") with input as swap(2, set("self-hosted", "homelab-${SITE}", [{"matchExpressions": [{"key": "node-role.kubernetes.io/control-plane", "operator": "Exists"}]}], sized))
}

test_terms_are_ored if {
	either := array.concat(keep_off, [{"matchExpressions": [{"key": "kubernetes.io/os", "operator": "Exists"}]}])
	denies(runner_affinity.deny, "would schedule onto a control-plane node") with input as swap(2, set("self-hosted", "homelab-${SITE}", either, sized))
}

test_an_affinity_that_admits_no_worker_is_refused if {
	nowhere := [{"matchExpressions": [{"key": "node-role.kubernetes.io/control-plane", "operator": "DoesNotExist"}, {"key": "dedicated", "operator": "Exists"}]}]
	denies(runner_affinity.deny, "would not schedule onto a worker either") with input as swap(2, set("self-hosted", "homelab-${SITE}", nowhere, sized))
}

test_no_required_affinity_is_refused if {
	denies(runner_affinity.deny, "has no required node affinity") with input as swap(2, set("self-hosted", "homelab-${SITE}", null, sized))
}

test_an_operator_the_rule_does_not_model_is_refused if {
	denies(runner_affinity.deny, `does not model the "In" operator`) with input as swap(2, set("self-hosted", "homelab-${SITE}", [{"matchExpressions": [{"key": "a", "operator": "In"}]}], sized))
}

test_no_scale_set_is_refused if {
	denies(runner_affinity.deny, "no runner scale set was found") with input as [controller, locals]
}
