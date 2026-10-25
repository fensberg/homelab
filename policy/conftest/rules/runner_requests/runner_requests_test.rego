package rules.runner_requests_test

import data.rules.runner_requests
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
	count(runner_requests.deny) == 0 with input as good
}

test_a_runner_with_no_memory_request_is_refused if {
	denies(runner_requests.deny, `container "runner" requests no memory`) with input as swap(2, set("self-hosted", "homelab-${SITE}", keep_off, {"cpu": "1"}))
}

test_no_scale_set_is_refused if {
	denies(runner_requests.deny, "no runner scale set was found") with input as [controller, locals]
}

test_a_scale_set_with_no_containers_is_refused if {
	bare := {"path": "clusters/core/bare.yaml", "contents": {"kind": "HelmRelease", "metadata": {"name": "bare"}, "spec": {"chart": {"spec": {"chart": "gha-runner-scale-set"}}, "values": {}}}}
	denies(runner_requests.deny, "the runner scale set bare declares no containers") with input as array.concat(good, [bare])
}
