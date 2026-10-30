# Every node gets a CNI agent, including one that is tainted.
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
#
# The rest of what the pod network is made of is its MTU (rules.cni_mtu) and
# where its addresses come from (rules.cni_ipam). That the cluster's health is
# not asked before the CNI is applied is the health gate's (rules.plan_health_gate).
package rules.cni

import data.lib
import rego.v1

agents := [f | some f in lib.objects; f.contents.kind == "DaemonSet"; f.contents.metadata.name == "cilium"]

deny contains msg if {
	count(agents) == 0
	msg := "no DaemonSet named cilium is declared, so this rule proves nothing. Either the manifest is empty or the agent was renamed."
}

deny contains msg if {
	some f in agents
	tolerations := object.get(f.contents, ["spec", "template", "spec", "tolerations"], [])
	not blanket(tolerations)
	msg := $"{f.path}: the cilium DaemonSet does not tolerate every taint. A tainted node would get no agent, and a node with no CNI never reaches Ready - so it would never join the cluster. The untrusted zone in docs/epochs/03-workload.md is a NoSchedule-tainted worker, so this is the line that lets it exist at all."
}

blanket(tolerations) if {
	some t in tolerations
	object.get(t, "key", "") == ""
	t.operator == "Exists"
}
