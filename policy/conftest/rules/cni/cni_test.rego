package rules.cni_test

import data.rules.cni
import rego.v1

agent(tolerations) := {"path": "clusters/bootstrap/cilium.yaml", "contents": {"kind": "DaemonSet", "metadata": {"name": "cilium"}, "spec": {"template": {"spec": {"tolerations": tolerations}}}}}

values(mtu) := {"path": "clusters/bootstrap/cilium-values.yaml", "contents": {"MTU": mtu, "ipam": {"mode": "kubernetes"}}}

config(d) := {"path": "clusters/bootstrap/cilium.yaml", "contents": {"kind": "ConfigMap", "metadata": {"name": "cilium-config"}, "data": d}}

good := [
	agent([{"operator": "Exists"}]),
	values(1450),
	config({"mtu": "1450", "ipam": "kubernetes"}),
]

with_one(i, f) := array.flatten([array.slice(good, 0, i), [f], array.slice(good, i + 1, count(good))])

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_the_cni_as_it_should_be_passes if {
	count(cni.deny) == 0 with input as good
}

test_an_agent_that_tolerates_one_taint_is_refused if {
	denies(cni.deny, "does not tolerate every taint") with input as with_one(0, agent([{"key": "node-role.kubernetes.io/control-plane", "operator": "Exists"}]))
}

test_no_agent_is_refused if {
	denies(cni.deny, "no DaemonSet named cilium") with input as [good[2]]
}

test_an_unpinned_mtu_is_refused if {
	denies(cni.deny, "pins no MTU") with input as with_one(1, {"path": "clusters/bootstrap/cilium-values.yaml", "contents": {}})
}

test_a_detected_size_mtu_is_refused if {
	denies(cni.deny, "carry no UDP") with input as with_one(1, values(1280))
}

test_a_render_that_disagrees_is_refused if {
	denies(cni.deny, "pins MTU 1450 and the committed manifest says 1500") with input as with_one(2, config({"mtu": "1500", "ipam": "kubernetes"}))
	denies(cni.deny, "carries no mtu") with input as with_one(2, config({"ipam": "kubernetes"}))
}

test_another_ipam_mode_is_refused if {
	denies(cni.deny, `not "kubernetes"`) with input as with_one(2, config({"mtu": "1450", "ipam": "cluster-pool"}))
	denies(cni.deny, "declares no ipam mode") with input as with_one(2, config({"mtu": "1450"}))
}
