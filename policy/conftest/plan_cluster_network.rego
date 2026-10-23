# Every machine is told the cluster's network: its pod and service ranges,
# declared, and the same on every machine of the site.
#
# WHAT WAS WRONG, twice. talos.tf once set no clusterNetwork fields, so the
# cluster's two largest address ranges were whatever Talos defaulted to
# (#240): correct, and chosen by nobody. Then the network patch sat on the
# control planes' configuration alone, so the workers took Talos's default
# range. A kubelet tells its pods to ask the tenth address of its own
# machine's serviceSubnets for DNS, so every pod on a worker asked
# 10.96.0.10 while cluster DNS answered in the site's own range: nothing on
# a worker could resolve a name, and site0's rebuild stopped at Health
# (#578). It was invisible for as long as the declared ranges equalled the
# defaults.
#
# ON THE PLAN. Each machine's configuration is the patches the plan would
# hand Talos for it, so every machine of every kind is held to it, however
# its patches are put together. clusterNetwork is fixed at cluster creation:
# a wrong value costs a rebuild, not an edit.
package rules.plan_cluster_network

import data.lib
import rego.v1

configurations contains [path, r] if {
	some [path, r] in lib.plan
	r.type == "talos_machine_configuration"
}

ranges := ["podSubnets", "serviceSubnets"]

# told is every value a machine's patches give one of the ranges.
told(r, name) := [v |
	some patch in object.get(r.values, "config_patches", [])
	is_string(patch)
	v := object.get(yaml.unmarshal(patch), ["cluster", "network", name], null)
	is_array(v)
	count(v) > 0
]

deny contains msg if {
	count(configurations) == 0
	msg := "no talos_machine_configuration is in any plan, so this checked nothing"
}

deny contains msg if {
	some [_, r] in configurations
	some name in ranges
	count(told(r, name)) == 0
	msg := sprintf("%s is not told the cluster's network: its configuration sets no cluster.network.%s. It then takes Talos's default range, and a kubelet tells every pod on it to ask for DNS at an address nothing answers on (#578). clusterNetwork is fixed at cluster creation, so the cost of getting it wrong is a rebuild.", [r.address, name])
}

deny contains msg if {
	some name in ranges
	some [path, a] in configurations
	some [other, b] in configurations
	path == other
	a.address < b.address
	count(told(a, name)) > 0
	count(told(b, name)) > 0
	told(a, name) != told(b, name)
	msg := sprintf("%s and %s are told different cluster.network.%s. One cluster has one pod and one service range, and a machine told another asks for DNS where nothing answers.", [a.address, b.address, name])
}
