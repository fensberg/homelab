package rules.runner_namespaces_test

import data.rules.runner_namespaces
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
	count(runner_namespaces.deny) == 0 with input as good
}

test_a_namespace_the_manifest_never_mentions_is_refused if {
	denies(runner_namespaces.deny, `local.runners_namespace = "arc-elsewhere"`) with input as swap(1, {"path": "modules/p/variables.tf", "contents": {"locals": [{"runner_system_namespace": "arc-systems", "runners_namespace": "arc-elsewhere"}]}})
}

test_a_namespace_declared_twice_is_refused if {
	again := {"path": "modules/q/variables.tf", "contents": {"locals": [{"runners_namespace": "arc-runners"}]}}
	denies(runner_namespaces.deny, "local.runners_namespace is declared 2 time(s)") with input as array.concat(good, [again])
}

test_no_controller_release_is_refused if {
	denies(runner_namespaces.deny, "0 HelmRelease named gha-runner-scale-set-controller were found") with input as array.slice(good, 1, count(good))
}
