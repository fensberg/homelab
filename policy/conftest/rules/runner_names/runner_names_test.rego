package rules.runner_names_test

import data.rules.runner_names
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
	count(runner_names.deny) == 0 with input as good
}

test_a_set_with_one_fixed_name_is_refused if {
	denies(runner_names.deny, "must end in ${SITE}") with input as swap(3, set("batch", "homelab-batch", keep_off, sized))
}

test_a_job_naming_a_site_by_hand_is_refused if {
	denies(runner_names.deny, "declares `runs-on: homelab-production`") with input as swap(4, workflow(["homelab-production", "homelab-batch-${{ matrix.site }}"]))
}

test_the_longest_prefix_decides_the_set if {
	denies(runner_names.deny, "registered per site, as homelab-batch-<site>") with input as swap(4, workflow(["homelab-${{ matrix.site }}", "homelab-batch-x"]))
}

test_a_set_nothing_asks_for_is_refused if {
	denies(runner_names.deny, "homelab-batch-<site>, so that set of runners serves nothing") with input as swap(4, workflow(["homelab-${{ matrix.site }}"]))
}

test_the_converges_set_missing_is_refused if {
	denies(runner_names.deny, "(self-hosted) is not among them") with input as array.concat(array.slice(good, 0, 2), array.slice(good, 3, 5))
}
