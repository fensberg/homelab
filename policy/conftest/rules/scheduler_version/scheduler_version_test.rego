package rules.scheduler_version_test

import data.rules.scheduler_version
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
	count(scheduler_version.deny) == 0 with input as good
}

test_a_scheduler_not_the_sites_is_refused if {
	denies(scheduler_version.deny, "a change is tried with Kubernetes 1.35.9 and a site runs 1.36.3") with input as with_one(0, env({"KUBERNETES_VERSION": "1.35.9"}))
}
