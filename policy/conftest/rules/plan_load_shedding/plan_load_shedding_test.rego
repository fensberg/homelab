package rules.plan_load_shedding_test

import data.rules.plan_load_shedding
import rego.v1

plan(configs) := {"path": "site-cluster.plan.json", "contents": {"planned_values": {"root_module": {"child_modules": [{"resources": configs}]}}}}

machine(kind, patches) := {"address": "m[\"100\"]", "type": "talos_machine_configuration", "mode": "data", "values": {"machine_type": kind, "config_patches": patches}}

sheds(system, soft, hard) := $"machine:\n  kubelet:\n    extraConfig:\n      systemReserved:\n        memory: {system}\n      evictionHard:\n        memory.available: {hard}\n      evictionSoft:\n        memory.available: {soft}\n      evictionSoftGracePeriod:\n        memory.available: 30s\n"

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_a_machine_that_declares_it_passes if {
	count(plan_load_shedding.deny) == 0 with input as [plan([machine("worker", [sheds("1088Mi", "256Mi", "100Mi"), null])])]
}

test_no_machine_at_all_is_refused if {
	denies(plan_load_shedding.deny, "checked nothing") with input as [plan([])]
}

test_a_machine_that_does_not_is_refused if {
	denies(plan_load_shedding.deny, "declares how it sheds load 0 times") with input as [plan([machine("worker", ["machine:\n  kubelet:\n    extraConfig:\n      maxPods: 110\n"])])]
}

test_holding_back_less_than_the_default_is_refused if {
	denies(plan_load_shedding.deny, "less than the kubelet's own default of 384Mi") with input as [plan([machine("worker", [sheds("256Mi", "256Mi", "100Mi")])])]
	denies(plan_load_shedding.deny, "less than the kubelet's own default of 512Mi") with input as [plan([machine("controlplane", [sheds("384Mi", "256Mi", "100Mi")])])]
}

test_a_gentle_threshold_at_the_hard_one_is_refused if {
	denies(plan_load_shedding.deny, "never put off gently") with input as [plan([machine("worker", [sheds("1088Mi", "100Mi", "100Mi")])])]
}

test_a_gentle_threshold_with_no_grace_is_refused if {
	denies(plan_load_shedding.deny, "and not for how long") with input as [plan([machine("worker", ["machine:\n  kubelet:\n    extraConfig:\n      systemReserved:\n        memory: 1088Mi\n      evictionHard:\n        memory.available: 100Mi\n      evictionSoft:\n        memory.available: 256Mi\n"])])]
}
