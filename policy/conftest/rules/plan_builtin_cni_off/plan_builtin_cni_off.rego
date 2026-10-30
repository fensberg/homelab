# Every machine's configuration turns the built-in CNI and kube-proxy off,
# both of them.
#
# These are two settings and one decision. Talos ships Flannel and
# kube-proxy; Cilium replaces both. Disabling one without the other leaves
# either two CNIs fighting over pod networking, or kube-proxy and Cilium both
# programming service routing - and the second is the quieter of the two
# failures. See docs/epochs/03-workload.md, "Cilium arrives from OpenTofu,
# between bootstrap and the health gate".
#
# ON THE PLAN. Each machine's configuration is the patches the plan would
# hand Talos for it, read as Talos reads them, so a machine of any kind,
# however its patches are put together, is judged on what it is sent. A
# patch the plan cannot know yet is not read: a setting moved into one is
# refused here as missing, never passed.
package rules.plan_builtin_cni_off

import data.lib
import rego.v1

configurations contains r if {
	some [_, r] in lib.plan
	r.type == "talos_machine_configuration"
}

# set is every value a machine's patches give a setting, by its path.
set(r, path) := [v |
	some patch in object.get(r.values, "config_patches", [])
	is_string(patch)
	doc := yaml.unmarshal(patch)
	v := object.get(doc, path, null)
	v != null
]

holds(r, path, want) if {
	values := set(r, path)
	count(values) > 0
	every v in values {
		v == want
	}
}

deny contains msg if {
	count(configurations) == 0
	msg := "no talos_machine_configuration is in any plan, so this checked nothing"
}

deny contains msg if {
	some r in configurations
	not holds(r, ["cluster", "network", "cni", "name"], "none")
	msg := $"{r.address}: the machine's configuration does not set cluster.network.cni.name = \"none\". Talos would then install Flannel, which enforces no NetworkPolicy - the whole reason this estate moved to Cilium. Two CNIs would also both claim pod networking."
}

deny contains msg if {
	some r in configurations
	not holds(r, ["cluster", "proxy", "disabled"], true)
	msg := $"{r.address}: the machine's configuration does not set cluster.proxy.disabled = true. Cilium is configured with kubeProxyReplacement, so leaving kube-proxy running means two things programming service routing on every node."
}
