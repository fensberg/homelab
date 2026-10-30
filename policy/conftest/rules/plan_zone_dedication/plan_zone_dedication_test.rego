package rules.plan_zone_dedication_test

import data.rules.plan_zone_dedication
import rego.v1

fact := {"path": "config/facts/untrusted-zone.json", "contents": {"key": "kept", "not_yet_clean": "kept/unclean"}}

kubelet(taints) := yaml.marshal({"machine": {"kubelet": {"extraArgs": {"register-with-taints": taints}}}})

labelled(labels) := yaml.marshal({"machine": {"nodeLabels": labels}})

machine(address, patches) := {"address": address, "mode": "data", "type": "talos_machine_configuration", "values": {"config_patches": patches}}

plan(machines) := {"path": "/tmp/plans/site/site0-cluster.plan.json", "contents": {"planned_values": {"root_module": {"resources": machines}}}}

estates := machine("data.talos_machine_configuration.worker[\"1\"]", [labelled({"region": "here"})])

zones(patches) := [fact, plan([estates, machine("data.talos_machine_configuration.zone[\"z\"]", patches)])]

kept := [kubelet("kept=z0:NoSchedule,kept/unclean=true:NoSchedule"), labelled({"kept": "z0", "region": "here"})]

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_a_zones_machine_tainted_labelled_and_closed_passes if {
	count(plan_zone_dedication.deny) == 0 with input as zones(kept)
}

test_a_machine_tainted_and_not_labelled_is_refused if {
	unlabelled := [kubelet("kept=z0:NoSchedule,kept/unclean=true:NoSchedule"), labelled({"zone": "z0"})]
	denies(plan_zone_dedication.deny, "cannot be sent there") with input as zones(unlabelled)
}

test_a_machine_labelled_and_not_tainted_is_refused if {
	untainted := [kubelet("kept/unclean=true:NoSchedule"), labelled({"kept": "z0"})]
	denies(plan_zone_dedication.deny, "not tainted kept=z0:NoSchedule") with input as zones(untainted)
}

test_a_taint_and_label_naming_two_zones_are_refused if {
	split_zone := [kubelet("kept=z1:NoSchedule,kept/unclean=true:NoSchedule"), labelled({"kept": "z0"})]
	denies(plan_zone_dedication.deny, "not tainted kept=z0:NoSchedule") with input as zones(split_zone)
}

test_a_zones_machine_open_before_it_is_clean_is_refused if {
	open := [kubelet("kept=z0:NoSchedule"), labelled({"kept": "z0"})]
	denies(plan_zone_dedication.deny, "takes work before it has a clean snapshot") with input as zones(open)
}

test_no_zones_machine_is_refused if {
	denies(plan_zone_dedication.deny, "no machine in a site's plan is kept for an untrusted zone") with input as [fact, plan([estates])]
}

test_no_fact_is_refused if {
	denies(plan_zone_dedication.deny, "gives no key") with input as [plan([estates])]
}

test_a_clean_taint_under_the_zones_own_key_is_refused if {
	same := {"path": "config/facts/untrusted-zone.json", "contents": {"key": "kept", "not_yet_clean": "kept"}}
	machines := plan([machine("data.talos_machine_configuration.zone[\"z\"]", [kubelet("kept=z0:NoSchedule,kept=true:NoSchedule"), labelled({"kept": "z0"})])])
	denies(plan_zone_dedication.deny, "would lift what keeps other work off the machine too") with input as [same, machines]
}
