# Cilium takes its pod addresses from Kubernetes, and that is load-bearing.
#
# This is the half worth guarding rather than the one #240 assumed. That issue
# warned that Cilium's default cluster pool is 10.0.0.0/8 and would collide
# with every site subnet - true of `ipam: cluster-pool`, and this estate runs
# `ipam: kubernetes`, which delegates to the podSubnets talos.tf declares and has
# no pool of its own.
#
# So the hazard is not live; it is one word away. Switching the mode would move
# address allocation from a range this repository declares to a default it does
# not, and the manifest would still render, still install, and still come up.
package rules.cni_ipam

import data.lib
import rego.v1

config := [f | some f in lib.objects; f.contents.kind == "ConfigMap"; f.contents.metadata.name == "cilium-config"]

deny contains msg if {
	count(config) != 1
	msg := $"{count(config)} ConfigMap named cilium-config were found, so which range pods are allocated from cannot be read"
}

deny contains msg if {
	msg := "the rendered Cilium manifest declares no ipam mode, so this cannot tell which range pods are allocated from"
	some f in config
	not f.contents.data.ipam
}

deny contains msg if {
	some f in config
	mode := f.contents.data.ipam
	mode != "kubernetes"
	msg := $"Cilium's ipam mode is \"{mode}\", not \"kubernetes\". In every other mode Cilium allocates pod addresses from a pool of its own, and its default pool is 10.0.0.0/8 - which contains every site subnet this estate can define, because site octets run 1-95. The podSubnets declared in talos.tf would then describe nothing. If the mode is being changed deliberately, the pool has to be declared with it and checked against the site scheme the same way, or this estate has gone back to an undeclared default."
}
