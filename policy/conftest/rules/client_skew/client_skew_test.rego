package rules.client_skew_test

import data.rules.client_skew
import rego.v1

env(over) := {"path": "scripts/versions.env", "contents": object.union({"TALOSCTL_VERSION": "v1.13.8", "TALOS_VERSION": "v1.13.9", "KUBECTL_VERSION": "v1.36.4", "KUBERNETES_VERSION": "1.36.3"}, over)}

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_pins_in_step_with_the_cluster_pass if {
	count(client_skew.deny) == 0 with input as [env({})]
}

test_a_talosctl_two_minors_away_is_refused if {
	denies(client_skew.deny, "Sidero supports a client within one minor") with input as [env({"TALOSCTL_VERSION": "v1.11.0"})]
}

test_a_cluster_two_minors_from_its_client_is_refused if {
	denies(client_skew.deny, "the cluster runs Talos v1.10.0") with input as [env({"TALOS_VERSION": "v1.10.0"})]
}

test_a_talosctl_of_another_major_is_refused if {
	denies(client_skew.deny, "talosctl is pinned to v2.13.0") with input as [env({"TALOSCTL_VERSION": "v2.13.0"})]
}

test_a_kubectl_two_minors_away_is_refused if {
	denies(client_skew.deny, "Kubernetes supports a client within one minor") with input as [env({"KUBECTL_VERSION": "v1.31.1"})]
}

test_a_pin_that_is_not_a_version_is_refused if {
	denies(client_skew.deny, "could not find TALOSCTL_VERSION") with input as [env({"TALOSCTL_VERSION": "latest"})]
	count(client_skew.deny) == 1 with input as [env({"TALOSCTL_VERSION": "latest"})]
	denies(client_skew.deny, "could not find TALOS_VERSION") with input as [env({"TALOS_VERSION": ""})]
	denies(client_skew.deny, "could not find KUBERNETES_VERSION") with input as [env({"KUBERNETES_VERSION": "1.36"})]
}

test_no_versions_file_is_refused if {
	denies(client_skew.deny, "could not find KUBECTL_VERSION") with input as []
}
