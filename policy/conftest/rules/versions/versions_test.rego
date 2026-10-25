package rules.versions_test

import data.rules.versions
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
	count(versions.deny) == 0 with input as good
}

test_a_talosctl_two_minors_away_is_refused if {
	denies(versions.deny, "Sidero supports a client within one minor") with input as with_one(0, env({"TALOSCTL_VERSION": "v1.11.0"}))
}

test_a_talosctl_of_another_major_is_refused if {
	denies(versions.deny, "talosctl is pinned to v2.13") with input as with_one(0, env({"TALOSCTL_VERSION": "v2.13.0"}))
}

test_a_kubectl_two_minors_away_is_refused if {
	denies(versions.deny, "Kubernetes supports a client within one minor") with input as with_one(0, env({"KUBECTL_VERSION": "v1.31.1"}))
}

test_a_cluster_version_declared_twice_is_refused if {
	twice := {"path": "modules/c/talos.tf", "contents": {"locals": [{"kubernetes_version": "1.31.1"}]}}
	denies(versions.deny, "kubernetes_version is declared as a version 2 time(s)") with input as array.concat(good, [twice])
}

test_a_restated_setup_version_is_refused if {
	denies(versions.deny, "restates a pinned version: go-version: 1.26.7") with input as with_one(2, workflow([{"with": {"go-version": "1.26.7"}}, {"with": {"node-version": "${{ env.NODE_VERSION }}"}}]))
}

test_too_few_setup_inputs_is_refused if {
	denies(versions.deny, "found only 1 version input(s)") with input as with_one(2, workflow([{"with": {"go-version": "${{ env.GO_VERSION }}"}}]))
}

test_a_pin_in_rclones_namespace_is_refused if {
	denies(versions.deny, "declares RCLONE_VERSION, which is in rclone's own configuration namespace") with input as with_one(0, env({"RCLONE_VERSION": "1.75.0"}))
}

test_the_inert_rclone_pin_missing_is_refused if {
	pins := object.remove(env({}).contents, ["RCLONE_DEB_VERSION"])
	denies(versions.deny, "no longer declares RCLONE_DEB_VERSION") with input as with_one(0, {"path": "scripts/versions.env", "contents": pins})
}

test_a_scheduler_not_the_sites_is_refused if {
	denies(versions.deny, "a change is tried with Kubernetes 1.35.9 and a site runs 1.36.3") with input as with_one(0, env({"KUBERNETES_VERSION": "1.35.9"}))
}
