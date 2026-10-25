package rules.cni_mtu_test

import data.rules.cni_mtu
import rego.v1

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

agent(tolerations) := {"path": "clusters/bootstrap/cilium.yaml", "contents": {"kind": "DaemonSet", "metadata": {"name": "cilium"}, "spec": {"template": {"spec": {"tolerations": tolerations}}}}}

values(mtu) := {"path": "clusters/bootstrap/cilium-values.yaml", "contents": {"MTU": mtu, "ipam": {"mode": "kubernetes"}}}

config(d) := {"path": "clusters/bootstrap/cilium.yaml", "contents": {"kind": "ConfigMap", "metadata": {"name": "cilium-config"}, "data": d}}

good := [
	agent([{"operator": "Exists"}]),
	values(1450),
	config({"mtu": "1450", "ipam": "kubernetes"}),
]

with_one(i, f) := array.flatten([array.slice(good, 0, i), [f], array.slice(good, i + 1, count(good))])

test_the_cni_as_it_should_be_passes if {
	count(cni_mtu.deny) == 0 with input as good
}

test_an_unpinned_mtu_is_refused if {
	denies(cni_mtu.deny, "pins no MTU") with input as with_one(1, {"path": "clusters/bootstrap/cilium-values.yaml", "contents": {}})
}

test_a_detected_size_mtu_is_refused if {
	denies(cni_mtu.deny, "carry no UDP") with input as with_one(1, values(1280))
}

test_a_render_that_disagrees_is_refused if {
	denies(cni_mtu.deny, "pins MTU 1450 and the committed manifest says 1500") with input as with_one(2, config({"mtu": "1500", "ipam": "kubernetes"}))
	denies(cni_mtu.deny, "carries no mtu") with input as with_one(2, config({"ipam": "kubernetes"}))
}
