# A rendered talosconfig carries the estate, so the operator does not have
# to: it names nodes, and every control plane as an endpoint.
#
# NODES. The Talos provider leaves them empty unless it is told otherwise,
# and an empty list is not a broken credential - it is a working credential
# on which every node-targeted command refuses on first use and has to be
# re-run with -n. Node addresses are what this repository keeps in the vault
# so the config shows the estate's shape without revealing it, so making the
# operator supply one puts back the piece the design took out (#235).
#
# ENDPOINTS. An endpoint is what talosctl connects to. Every control plane
# proxies the Talos API, so a credential naming one of them is useless
# precisely when that machine is the one being diagnosed.
#
# The real refusal of a talosconfig that names no nodes is the contractor's,
# on what OpenTofu produced, at the moment of use. This one fires on the
# branch that removed the argument, which is cheaper by a whole incident.
#
# ON THE PLAN: what the credential would be rendered with, against the
# number of control planes the plan configures. It says nothing of which
# addresses: those are a decision this has no business holding.
package rules.plan_talosconfig

import data.lib
import rego.v1

credentials contains [path, r] if {
	some [path, r] in lib.plan
	r.type == "talos_client_configuration"
}

control_planes(path) := count([r |
	some [path, r] in lib.plan
	r.type == "talos_machine_configuration"
	r.values.machine_type == "controlplane"
])

deny contains msg if {
	count(credentials) == 0
	msg := "no talos_client_configuration is in any plan, so this checked nothing"
}

deny contains msg if {
	some [_, r] in credentials
	count(object.get(r.values, "nodes", [])) == 0
	msg := sprintf("%s renders a talosconfig that sets no nodes. It is a working credential on which every node-targeted command refuses on first use and has to be re-run with -n, and the node addresses are the vault's to supply, not the operator's (#235).", [r.address])
}

deny contains msg if {
	some [path, r] in credentials
	count(object.get(r.values, "endpoints", [])) != control_planes(path)
	msg := sprintf("%s renders a talosconfig with %d endpoint(s), and the site has %d control plane(s). Every control plane proxies the Talos API, so naming fewer loses the credential the moment one of them is the machine being diagnosed.", [r.address, count(object.get(r.values, "endpoints", [])), control_planes(path)])
}
