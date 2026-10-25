package rules.rclone_namespace_test

import data.rules.rclone_namespace
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
	count(rclone_namespace.deny) == 0 with input as good
}

test_a_pin_in_rclones_namespace_is_refused if {
	denies(rclone_namespace.deny, "declares RCLONE_VERSION, which is in rclone's own configuration namespace") with input as with_one(0, env({"RCLONE_VERSION": "1.75.0"}))
}

test_the_inert_rclone_pin_missing_is_refused if {
	pins := object.remove(env({}).contents, ["RCLONE_DEB_VERSION"])
	denies(rclone_namespace.deny, "no longer declares RCLONE_DEB_VERSION") with input as with_one(0, {"path": "scripts/versions.env", "contents": pins})
}
