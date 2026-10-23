# Every machine's configuration declares KubePrism, on the port Cilium is
# told to reach the API through.
#
# With kube-proxy gone, every Cilium agent reaches the API server through
# KubePrism on localhost. That is load-bearing: the alternative address is the
# cluster endpoint, which this estate hardcodes to one control plane (#316),
# and using it would turn a known API single point of failure into a
# pod-network one. Talos enables KubePrism by default, which is exactly why it
# is written down: a default is not a declaration, and the day it changes the
# pod network fails with nothing here ever having said it was required.
#
# ON THE PLAN, against the values the CNI was rendered from: the port each
# machine is sent and the k8sServicePort in cilium-values.yaml are one fact,
# and if it moves every half has to move with it.
package rules.plan_kubeprism

import data.lib
import rego.v1

configurations contains r if {
	some [_, r] in lib.plan
	r.type == "talos_machine_configuration"
}

prisms(r) := [p |
	some patch in object.get(r.values, "config_patches", [])
	is_string(patch)
	p := object.get(yaml.unmarshal(patch), ["machine", "features", "kubePrism"], null)
	p != null
]

ports contains f.contents.k8sServicePort if {
	some f in lib.named("cilium-values.yaml")
	is_number(f.contents.k8sServicePort)
}

deny contains msg if {
	count(configurations) == 0
	msg := "no talos_machine_configuration is in any plan, so this checked nothing"
}

deny contains msg if {
	count(ports) != 1
	msg := sprintf("the CNI's values name %d k8sServicePort(s), and the port KubePrism must serve is one", [count(ports)])
}

deny contains msg if {
	some r in configurations
	count(prisms(r)) == 0
	msg := sprintf("%s never declares KubePrism. With kube-proxy gone there is no ClusterIP for its agent to reach the API through, and the alternative address is one control plane (#316). It is enabled by default, which is exactly why it is written down: a default is not a declaration.", [r.address])
}

deny contains msg if {
	some r in configurations
	some p in prisms(r)
	object.get(p, "enabled", false) != true
	msg := sprintf("%s declares KubePrism and does not enable it, so its agent has no local way to the API.", [r.address])
}

deny contains msg if {
	some r in configurations
	some p in prisms(r)
	some port in ports
	object.get(p, "port", null) != port
	msg := sprintf("%s declares KubePrism on port %v, and the CNI's values point k8sServicePort at %v. The two are one fact: if the port moves, every half has to move with it.", [r.address, object.get(p, "port", null), port])
}
