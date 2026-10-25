package rules.client_skew_test

import data.rules.client_skew
import rego.v1

env(over) := {"path": "scripts/versions.env", "contents": object.union({"TALOSCTL_VERSION": "v1.13.8", "KUBECTL_VERSION": "v1.36.4", "KUBERNETES_VERSION": "1.36.3", "RCLONE_DEB_VERSION": "1.75.0"}, over)}

module(talos, kubernetes) := {"path": "modules/c/variables.tf", "contents": {"locals": [{"talos_version": talos}, {"kubernetes_version": kubernetes}]}}

workflow(steps) := {"path": ".github/workflows/w.yml", "contents": {"jobs": {"j": {"steps": steps}}}}

reads := workflow([{"with": {"go-version": "${{ env.GO_VERSION }}"}}, {"with": {"node-version": "${{ env.NODE_VERSION }}"}}])

good := [env({}), module("v1.13.9", "1.36.3"), reads]

with_one(i, f) := array.flatten([array.slice(good, 0, i), [f], array.slice(good, i + 1, count(good))])

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_pins_in_step_with_the_cluster_pass if {
	count(client_skew.deny) == 0 with input as good
}

test_a_talosctl_two_minors_away_is_refused if {
	denies(client_skew.deny, "Sidero supports a client within one minor") with input as with_one(0, env({"TALOSCTL_VERSION": "v1.11.0"}))
}

test_a_talosctl_of_another_major_is_refused if {
	denies(client_skew.deny, "talosctl is pinned to v2.13") with input as with_one(0, env({"TALOSCTL_VERSION": "v2.13.0"}))
}

test_a_kubectl_two_minors_away_is_refused if {
	denies(client_skew.deny, "Kubernetes supports a client within one minor") with input as with_one(0, env({"KUBECTL_VERSION": "v1.31.1"}))
}

test_a_cluster_version_declared_twice_is_refused if {
	twice := {"path": "modules/c/talos.tf", "contents": {"locals": [{"kubernetes_version": "1.31.1"}]}}
	denies(client_skew.deny, "kubernetes_version is declared as a version 2 time(s)") with input as array.concat(good, [twice])
}

test_no_talosctl_pin_is_refused if {
	denies(client_skew.deny, "could not find TALOSCTL_VERSION") with input as with_one(0, env({"TALOSCTL_VERSION": "latest"}))
}

test_no_kubectl_pin_is_refused if {
	denies(client_skew.deny, "could not find KUBECTL_VERSION") with input as with_one(0, env({"KUBECTL_VERSION": "latest"}))
}

test_a_talos_version_declared_twice_is_refused if {
	twice := {"path": "modules/d/variables.tf", "contents": {"locals": [{"talos_version": "v1.13.9"}]}}
	denies(client_skew.deny, "talos_version is declared as a version 2 time(s)") with input as array.concat(good, [twice])
}
