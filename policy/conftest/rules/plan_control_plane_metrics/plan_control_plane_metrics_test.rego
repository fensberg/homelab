package rules.plan_control_plane_metrics_test

import data.rules.plan_control_plane_metrics
import rego.v1

chart(on) := {"path": "clusters/m.yaml", "contents": {"kind": "HelmRelease", "metadata": {"name": "kube-prometheus-stack"}, "spec": {"values": {c: {"enabled": c in on} | some c in ["kubeScheduler", "kubeControllerManager", "kubeEtcd"]}}}}

machine(address, kind, patches) := {"address": address, "type": "talos_machine_configuration", "mode": "data", "values": {"machine_type": kind, "config_patches": patches}}

plan(machines) := {"path": "site-cluster.plan.json", "contents": {"planned_values": {"root_module": {"child_modules": [{"resources": machines}]}}}}

all := ["kubeScheduler", "kubeControllerManager", "kubeEtcd"]

exposing := "cluster:\n  scheduler:\n    extraArgs:\n      bind-address: 0.0.0.0\n  controllerManager:\n    extraArgs:\n      bind-address: 0.0.0.0\n  etcd:\n    extraArgs:\n      listen-metrics-urls: http://0.0.0.0:2381\n"

schedulers_only := "cluster:\n  scheduler:\n    extraArgs:\n      bind-address: 0.0.0.0\n  controllerManager:\n    extraArgs:\n      bind-address: 0.0.0.0\n"

cps(patch) := plan([machine("cp[\"100\"]", "controlplane", [patch, null]), machine("cp[\"101\"]", "controlplane", [patch]), machine("w[\"200\"]", "worker", ["machine: {}\n"])])

denies(i, words) if {
	some msg in plan_control_plane_metrics.deny with input as i
	contains(msg, words)
}

test_both_halves_together_pass if {
	count(plan_control_plane_metrics.deny) == 0 with input as [chart(all), cps(exposing)]
}

test_neither_half_passes if {
	count(plan_control_plane_metrics.deny) == 0 with input as [chart([]), cps("machine: {}\n")]
}

test_the_chart_alone_is_refused if {
	denies([chart(all), cps(schedulers_only)], "not every control plane's configuration sets cluster.etcd.extraArgs.listen-metrics-urls")
}

test_one_control_plane_left_out_is_refused if {
	i := [chart(all), plan([machine("cp[\"100\"]", "controlplane", [exposing]), machine("cp[\"101\"]", "controlplane", [schedulers_only])])]
	denies(i, "not every control plane's configuration sets cluster.etcd.extraArgs.listen-metrics-urls")
}

test_the_machine_configuration_alone_is_refused if {
	denies([chart(["kubeScheduler", "kubeControllerManager"]), cps(exposing)], "the chart does not scrape kubeEtcd")
}

test_a_chart_that_says_nothing_is_refused if {
	i := [{"path": "clusters/m.yaml", "contents": {"kind": "HelmRelease", "metadata": {"name": "kube-prometheus-stack"}, "spec": {"values": {}}}}, cps(exposing)]
	denies(i, "says nothing about kubeEtcd at all")
}

test_no_control_plane_is_refused if {
	denies([chart(all), plan([])], "no control plane's machine configuration")
}
