package rules.cni_test

import data.rules.cni
import rego.v1

gate(deps) := {"path": "modules/c/talos.tf", "contents": {"data": {"talos_cluster_health": {"this": [{"depends_on": deps}]}}}}

agent(tolerations) := {"path": "clusters/bootstrap/cilium.yaml", "contents": {"kind": "DaemonSet", "metadata": {"name": "cilium"}, "spec": {"template": {"spec": {"tolerations": tolerations}}}}}

values(mtu) := {"path": "clusters/bootstrap/cilium-values.yaml", "contents": {"MTU": mtu, "ipam": {"mode": "kubernetes"}}}

config(d) := {"path": "clusters/bootstrap/cilium.yaml", "contents": {"kind": "ConfigMap", "metadata": {"name": "cilium-config"}, "data": d}}

good := [
	gate(["${talos_cluster_kubeconfig.this}", "${terraform_data.cilium}"]),
	agent([{"operator": "Exists"}]),
	values(1450),
	config({"mtu": "1450", "ipam": "kubernetes"}),
]

with_one(i, f) := array.concat(array.concat(array.slice(good, 0, i), [f]), array.slice(good, i + 1, count(good)))

denies(i, words) if {
	some msg in cni.deny with input as i
	contains(msg, words)
}

test_the_cni_as_it_should_be_passes if {
	count(cni.deny) == 0 with input as good
}

test_a_gate_not_waiting_for_the_cni_is_refused if {
	denies(with_one(0, gate(["${talos_cluster_kubeconfig.this}"])), "does not depend on terraform_data.cilium")
}

test_an_agent_that_tolerates_one_taint_is_refused if {
	denies(with_one(1, agent([{"key": "node-role.kubernetes.io/control-plane", "operator": "Exists"}])), "does not tolerate every taint")
}

test_no_agent_is_refused if {
	denies([good[0], good[3]], "no DaemonSet named cilium")
}

test_an_unpinned_mtu_is_refused if {
	denies(with_one(2, {"path": "clusters/bootstrap/cilium-values.yaml", "contents": {}}), "pins no MTU")
}

test_a_detected_size_mtu_is_refused if {
	denies(with_one(2, values(1280)), "carry no UDP")
}

test_a_render_that_disagrees_is_refused if {
	denies(with_one(3, config({"mtu": "1500", "ipam": "kubernetes"})), "pins MTU 1450 and the committed manifest says 1500")
	denies(with_one(3, config({"ipam": "kubernetes"})), "carries no mtu")
}

test_another_ipam_mode_is_refused if {
	denies(with_one(3, config({"mtu": "1450", "ipam": "cluster-pool"})), `not "kubernetes"`)
	denies(with_one(3, config({"mtu": "1450"})), "declares no ipam mode")
}
