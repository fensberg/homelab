# The CNI: what the cluster's pod network is made of. That the cluster's
# health is not asked before the CNI is applied is the health gate's
# (policy/conftest/rules/plan_health_gate/plan_health_gate.rego).
package rules.cni

import data.lib
import rego.v1

# Every node gets an agent, including one that is tainted.
#
# This is the DMZ node's readiness, asserted before that node exists.
#
# docs/epochs/03-workload.md puts the untrusted workload on a dedicated worker
# carrying a NoSchedule taint, with the toleration held only by workloads in
# that zone - deliberately, so the scheduler cannot quietly widen the blast
# radius by putting something else there. A CNI is the one thing that must
# ignore that rule: a node with no agent has no pod network, never reaches
# Ready, and never joins the cluster at all.
#
# So the agent DaemonSet tolerating every taint is load-bearing rather than
# sloppy. This estate has refused a blanket `operator: Exists` toleration
# before - on an unpinned debug image proposed for a control plane - and the
# distinction is worth stating: there it was a convenience that widened where
# untrusted code could run, here it is the condition on a machine having a
# network. If a chart version or a values change ever drops it, the DMZ node
# will simply never come up, and nothing else in this repository would say why.

agents := [f | some f in lib.objects; f.contents.kind == "DaemonSet"; f.contents.metadata.name == "cilium"]

deny contains msg if {
	count(agents) == 0
	msg := "no DaemonSet named cilium is declared, so this rule proves nothing. Either the manifest is empty or the agent was renamed."
}

deny contains msg if {
	some f in agents
	tolerations := object.get(f.contents, ["spec", "template", "spec", "tolerations"], [])
	not blanket(tolerations)
	msg := sprintf("%s: the cilium DaemonSet does not tolerate every taint. A tainted node would get no agent, and a node with no CNI never reaches Ready - so it would never join the cluster. The untrusted zone in docs/epochs/03-workload.md is a NoSchedule-tainted worker, so this is the line that lets it exist at all.", [f.path])
}

blanket(tolerations) if {
	some t in tolerations
	object.get(t, "key", "") == ""
	t.operator == "Exists"
}

# The pod MTU is pinned, and the rendered manifest carries the number.
#
# Since v1.17 Cilium hands every pod the LOWEST link MTU it finds on the node.
# These nodes carry `tailscale0` at 1280, which is not how a pod reaches
# anything: pod traffic leaves over eth0 at 1450. Detection therefore sizes
# every pod for an interface it does not use.
#
# The damage is not only lost throughput. 1280 minus Cilium's tunnel overhead
# is 1230, below the 1252-byte initial packet quic-go insists on, so every
# QUIC handshake from the tunnel connector timed out and it fell back to
# HTTP/2 - which carries no UDP, and so cannot serve the game server (#455).
# Cilium tracks the regression as cilium/cilium#37529, open and unfixed on
# 1.18, and `MTU` is the escape hatch its maintainers point at.
#
# So the value is declared, and the committed render has to agree with it: a
# value edited without `task render-cni` is a pin that changes nothing, and
# the cluster keeps whatever the last render said.

dir(path) := concat("/", array.slice(parts, 0, count(parts) - 1)) if parts := split(path, "/")

config := [f | some f in lib.objects; f.contents.kind == "ConfigMap"; f.contents.metadata.name == "cilium-config"]

values := [f |
	some agent in agents
	some f in lib.named("cilium-values.yaml")
	f.path == sprintf("%s/cilium-values.yaml", [dir(agent.path)])
]

deny contains msg if {
	count(agents) > 0
	count(values) == 0
	msg := "no cilium-values.yaml is beside the rendered CNI, so the values it was rendered from cannot be read"
}

deny contains msg if {
	some f in values
	not is_number(object.get(f.contents, "MTU", null))
	msg := sprintf("%s pins no MTU. Cilium then detects one, and on these nodes it detects tailscale0's 1280 and gives it to every pod. That is below what quic-go will start a connection with once Cilium's tunnel overhead comes off it, so the tunnel connector cannot use QUIC, and without QUIC it carries no UDP at all (#455, cilium/cilium#37529). Pin MTU to the MTU of the interface pods actually leave by, and re-render.", [f.path])
}

deny contains msg if {
	some f in values
	f.contents.MTU <= 1280
	msg := sprintf("the pinned MTU is %d, which leaves less than quic-go's 1252-byte initial packet once Cilium's tunnel overhead comes off it. The tunnel connector would fall back to HTTP/2 and carry no UDP (#455).", [f.contents.MTU])
}

deny contains msg if {
	count(config) != 1
	msg := sprintf("%d ConfigMap named cilium-config were found, so what the committed render runs cannot be read", [count(config)])
}

deny contains msg if {
	some f in config
	not f.contents.data.mtu
	msg := sprintf("%s carries no mtu in its config, so the pinned value never reached the manifest. Re-render with `task render-cni`.", [f.path])
}

deny contains msg if {
	some v in values
	some f in config
	rendered := f.contents.data.mtu
	rendered != sprintf("%v", [v.contents.MTU])
	msg := sprintf("cilium-values.yaml pins MTU %v and the committed manifest says %s. The cluster runs the manifest, so the pin is not in effect. Re-render with `task render-cni`.", [v.contents.MTU, rendered])
}

# Cilium takes its pod addresses from Kubernetes, and that is load-bearing.
#
# This is the half worth guarding rather than the one #240 assumed. That issue
# warned that Cilium's default cluster pool is 10.0.0.0/8 and would collide
# with every site subnet - true of `ipam: cluster-pool`, and this estate runs
# `ipam: kubernetes`, which delegates to the podSubnets declared above and has
# no pool of its own.
#
# So the hazard is not live; it is one word away. Switching the mode would move
# address allocation from a range this repository declares to a default it does
# not, and the manifest would still render, still install, and still come up.

deny contains msg if {
	msg := "the rendered Cilium manifest declares no ipam mode, so this cannot tell which range pods are allocated from"
	some f in config
	not f.contents.data.ipam
}

deny contains msg if {
	some f in config
	mode := f.contents.data.ipam
	mode != "kubernetes"
	msg := sprintf("Cilium's ipam mode is %q, not \"kubernetes\". In every other mode Cilium allocates pod addresses from a pool of its own, and its default pool is 10.0.0.0/8 - which contains every site subnet this estate can define, because site octets run 1-95. The podSubnets declared in talos.tf would then describe nothing. If the mode is being changed deliberately, the pool has to be declared with it and checked against the site scheme the same way, or this estate has gone back to an undeclared default.", [mode])
}
