package rules.plan_builtin_cni_off_test

import data.rules.plan_builtin_cni_off
import rego.v1

plan(configs) := {"path": "site-cluster.plan.json", "contents": {"planned_values": {"root_module": {"child_modules": [{"resources": configs}]}}}}

machine(address, patches) := {"address": address, "type": "talos_machine_configuration", "mode": "data", "values": {"config_patches": patches}}

off := "cluster:\n  network:\n    cni:\n      name: none\n  proxy:\n    disabled: true\n"

denies(i, words) if {
	some msg in plan_builtin_cni_off.deny with input as i
	contains(msg, words)
}

test_a_machine_with_both_off_passes if {
	count(plan_builtin_cni_off.deny) == 0 with input as [plan([machine("m[\"100\"]", [off, null, "machine:\n  install:\n    disk: /dev/sda\n"])])]
}

test_flannel_is_refused if {
	denies([plan([machine("m[\"100\"]", ["cluster:\n  network:\n    cni:\n      name: flannel\n  proxy:\n    disabled: true\n"])])], "m[\"100\"]: the machine's configuration does not set cluster.network.cni.name")
}

test_kube_proxy_left_on_is_refused if {
	denies([plan([machine("m[\"100\"]", ["cluster:\n  network:\n    cni:\n      name: none\n"])])], "cluster.proxy.disabled = true")
}

test_a_later_patch_turning_it_back_on_is_refused if {
	denies([plan([machine("m[\"100\"]", [off, "cluster:\n  proxy:\n    disabled: false\n"])])], "cluster.proxy.disabled = true")
}

test_a_setting_only_in_a_patch_not_yet_known_is_refused if {
	denies([plan([machine("m[\"200\"]", [null])])], "cluster.network.cni.name")
}

test_no_machine_configuration_is_refused if {
	denies([plan([])], "checked nothing")
}
