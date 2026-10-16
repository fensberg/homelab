package runners_test

import data.runners
import rego.v1

keep_off := [{"matchExpressions": [{"key": "node-role.kubernetes.io/control-plane", "operator": "DoesNotExist"}]}]

set(name, scale_set, terms, requests) := {"path": sprintf("clusters/core/%s.yaml", [name]), "contents": {"kind": "HelmRelease", "metadata": {"name": name, "namespace": "arc-runners"}, "spec": {"chart": {"spec": {"chart": "gha-runner-scale-set"}}, "values": {"runnerScaleSetName": scale_set, "template": {"spec": {"affinity": {"nodeAffinity": {"requiredDuringSchedulingIgnoredDuringExecution": {"nodeSelectorTerms": terms}}}, "containers": [{"name": "runner", "resources": {"requests": requests}}]}}}}}}

sized := {"cpu": "1", "memory": "2Gi"}

controller := {"path": "clusters/core/controller.yaml", "contents": {"kind": "HelmRelease", "metadata": {"name": "gha-runner-scale-set-controller", "namespace": "arc-systems"}}}

locals := {"path": "modules/p/variables.tf", "contents": {"locals": [{"runner_system_namespace": "arc-systems", "runners_namespace": "arc-runners"}]}}

workflow(labels) := {"path": ".github/workflows/w.yml", "contents": {"jobs": {sprintf("j%d", [i]): {"runs-on": l} | some i, l in labels}}}

good := [
	controller,
	locals,
	set("self-hosted", "homelab-${SITE}", keep_off, sized),
	set("batch", "homelab-batch-${SITE}", keep_off, sized),
	workflow(["homelab-${{ matrix.site }}", "homelab-batch-${{ matrix.site }}", "ubuntu-latest"]),
]

swap(i, f) := array.concat(array.concat(array.slice(good, 0, i), [f]), array.slice(good, i + 1, count(good)))

denies(i, words) if {
	some msg in runners.deny with input as i
	contains(msg, words)
}

test_the_runners_as_they_should_be_pass if {
	count(runners.deny) == 0 with input as good
}

test_a_namespace_the_manifest_never_mentions_is_refused if {
	denies(swap(1, {"path": "modules/p/variables.tf", "contents": {"locals": [{"runner_system_namespace": "arc-systems", "runners_namespace": "arc-elsewhere"}]}}), `local.runners_namespace = "arc-elsewhere"`)
}

test_a_set_with_one_fixed_name_is_refused if {
	denies(swap(3, set("batch", "homelab-batch", keep_off, sized)), "must end in ${SITE}")
}

test_a_job_naming_a_site_by_hand_is_refused if {
	denies(swap(4, workflow(["homelab-production", "homelab-batch-${{ matrix.site }}"])), "declares `runs-on: homelab-production`")
}

test_the_longest_prefix_decides_the_set if {
	denies(swap(4, workflow(["homelab-${{ matrix.site }}", "homelab-batch-x"])), "registered per site, as homelab-batch-<site>")
}

test_a_set_nothing_asks_for_is_refused if {
	denies(swap(4, workflow(["homelab-${{ matrix.site }}"])), "homelab-batch-<site>, so that set of runners serves nothing")
}

test_a_runner_admitted_to_a_control_plane_is_refused if {
	denies(swap(2, set("self-hosted", "homelab-${SITE}", [{"matchExpressions": [{"key": "node-role.kubernetes.io/control-plane", "operator": "Exists"}]}], sized)), "would schedule onto a control-plane node")
}

test_terms_are_ored if {
	either := array.concat(keep_off, [{"matchExpressions": [{"key": "kubernetes.io/os", "operator": "Exists"}]}])
	denies(swap(2, set("self-hosted", "homelab-${SITE}", either, sized)), "would schedule onto a control-plane node")
}

test_an_affinity_that_admits_no_worker_is_refused if {
	nowhere := [{"matchExpressions": [{"key": "node-role.kubernetes.io/control-plane", "operator": "DoesNotExist"}, {"key": "dedicated", "operator": "Exists"}]}]
	denies(swap(2, set("self-hosted", "homelab-${SITE}", nowhere, sized)), "would not schedule onto a worker either")
}

test_no_required_affinity_is_refused if {
	denies(swap(2, set("self-hosted", "homelab-${SITE}", null, sized)), "has no required node affinity")
}

test_an_operator_the_rule_does_not_model_is_refused if {
	denies(swap(2, set("self-hosted", "homelab-${SITE}", [{"matchExpressions": [{"key": "a", "operator": "In"}]}], sized)), `does not model the "In" operator`)
}

test_a_runner_with_no_memory_request_is_refused if {
	denies(swap(2, set("self-hosted", "homelab-${SITE}", keep_off, {"cpu": "1"})), `container "runner" requests no memory`)
}

test_the_converges_set_missing_is_refused if {
	denies(array.concat(array.slice(good, 0, 2), array.slice(good, 3, 5)), "(self-hosted) is not among them")
}
