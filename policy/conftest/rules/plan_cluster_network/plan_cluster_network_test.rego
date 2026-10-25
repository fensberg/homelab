package rules.plan_cluster_network_test

import data.rules.plan_cluster_network
import rego.v1

plan(configs) := {"path": "site-cluster.plan.json", "contents": {"planned_values": {"root_module": {"child_modules": [{"resources": configs}]}}}}

machine(address, patches) := {"address": address, "type": "talos_machine_configuration", "mode": "data", "values": {"config_patches": patches}}

network(pods, services) := sprintf("cluster:\n  network:\n    podSubnets: [%s]\n    serviceSubnets: [%s]\n", [pods, services])

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_every_machine_told_the_same_passes if {
	count(plan_cluster_network.deny) == 0 with input as [plan([machine("cp[\"100\"]", [network("10.110.0.0/16", "10.196.40.0/22"), null]), machine("w[\"200\"]", [network("10.110.0.0/16", "10.196.40.0/22")])])]
}

test_a_machine_told_nothing_is_refused if {
	denies(plan_cluster_network.deny, "w[\"200\"] is not told the cluster's network: its configuration sets no cluster.network.podSubnets") with input as [plan([machine("cp[\"100\"]", [network("10.110.0.0/16", "10.196.40.0/22")]), machine("w[\"200\"]", ["machine:\n  install:\n    disk: /dev/sda\n"])])]
}

test_a_machine_without_services_is_refused if {
	denies(plan_cluster_network.deny, "sets no cluster.network.serviceSubnets") with input as [plan([machine("w[\"200\"]", ["cluster:\n  network:\n    podSubnets: [10.110.0.0/16]\n"])])]
}

test_machines_told_different_ranges_are_refused if {
	denies(plan_cluster_network.deny, "cp[\"100\"] and w[\"200\"] are told different cluster.network.serviceSubnets") with input as [plan([machine("cp[\"100\"]", [network("10.110.0.0/16", "10.196.40.0/22")]), machine("w[\"200\"]", [network("10.110.0.0/16", "10.96.0.0/12")])])]
}

test_no_machine_configuration_is_refused if {
	denies(plan_cluster_network.deny, "checked nothing") with input as [plan([])]
}
