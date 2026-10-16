package control_plane_metrics_test

import data.control_plane_metrics
import rego.v1

chart(on) := {"path": "clusters/m.yaml", "contents": {"kind": "HelmRelease", "metadata": {"name": "kube-prometheus-stack"}, "spec": {"values": {c: {"enabled": c in on} | some c in ["kubeScheduler", "kubeControllerManager", "kubeEtcd"]}}}}

talos(lines) := {"path": "modules/x/talos.tf", "contents": {"data": {"talos_machine_configuration": {"controlplane": [{"config_patches": [sprintf("${yamlencode({\n%s\n})}", [concat("\n", lines)])]}]}}}}

all := ["kubeScheduler", "kubeControllerManager", "kubeEtcd"]

exposing := ["bind-address = \"0.0.0.0\"", "listen-metrics-urls = \"http://0.0.0.0:2381\""]

test_both_halves_together_pass if {
	count(control_plane_metrics.deny) == 0 with input as [chart(all), talos(exposing)]
}

test_neither_half_passes if {
	count(control_plane_metrics.deny) == 0 with input as [chart([]), talos([])]
}

test_the_chart_alone_is_refused if {
	some msg in control_plane_metrics.deny with input as [chart(all), talos(["bind-address = \"0.0.0.0\""])]
	contains(msg, `does not contain "listen-metrics-urls"`)
}

test_a_comment_is_not_the_machine_configuration if {
	some msg in control_plane_metrics.deny with input as [chart(all), talos(["bind-address = \"0.0.0.0\"", "# listen-metrics-urls = \"http://0.0.0.0:2381\""])]
	contains(msg, `does not contain "listen-metrics-urls"`)
}

test_the_machine_configuration_alone_is_refused if {
	some msg in control_plane_metrics.deny with input as [chart(["kubeScheduler", "kubeControllerManager"]), talos(exposing)]
	contains(msg, "the chart does not scrape kubeEtcd")
}

test_a_chart_that_says_nothing_is_refused if {
	i := [{"path": "clusters/m.yaml", "contents": {"kind": "HelmRelease", "metadata": {"name": "kube-prometheus-stack"}, "spec": {"values": {}}}}, talos(exposing)]
	some msg in control_plane_metrics.deny with input as i
	contains(msg, "says nothing about kubeEtcd at all")
}

test_no_chart_is_refused if {
	some msg in control_plane_metrics.deny with input as [talos(exposing)]
	contains(msg, "0 HelmRelease named kube-prometheus-stack")
}
