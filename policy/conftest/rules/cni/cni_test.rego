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
