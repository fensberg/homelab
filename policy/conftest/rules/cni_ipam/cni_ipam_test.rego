package rules.cni_ipam_test

import data.rules.cni_ipam
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
	count(cni_ipam.deny) == 0 with input as good
}

test_another_ipam_mode_is_refused if {
	denies(cni_ipam.deny, `not "kubernetes"`) with input as with_one(2, config({"mtu": "1450", "ipam": "cluster-pool"}))
	denies(cni_ipam.deny, "declares no ipam mode") with input as with_one(2, config({"mtu": "1450"}))
}
