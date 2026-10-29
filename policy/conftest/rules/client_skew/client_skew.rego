# Each client the estate pins is within one minor of the cluster it talks to:
# talosctl of Talos, and kubectl of Kubernetes.
#
# talosctl is a client for a specific Talos version. Sidero's own guidance is
# to keep it within one minor of the cluster it talks to. kubectl is the same
# for Kubernetes, which supports a client within one minor of the API server
# in either direction. Each pair is two lines of scripts/versions.env, which
# the cluster's module reads its versions from, moved by different people at
# different times - the exact shape of drift that goes unnoticed until a
# command behaves oddly during an incident.
#
# It had drifted, in both directions at once. The cluster ran Kubernetes
# 1.31.1 pinned inline in talos.tf where nothing watched it, versions.env said
# 1.34.11, and the workstation installer fetched whatever upstream called
# stable - three numbers, no two of them within a minor of each other.
#
# This is another gotcha turned into a rule: it was found by reading
# install-dependencies.sh while writing a rotation runbook, not by anything
# failing, which is precisely why it deserves a check rather than a note.
package rules.client_skew

import data.lib
import rego.v1

pins := object.union_n([f.contents | some f in lib.at("scripts/versions.env")])

# Each client, the cluster it talks to, and what drifting past a minor costs.
pairs := [
	{
		"client": "talosctl", "client_key": "TALOSCTL_VERSION",
		"cluster": "Talos", "cluster_key": "TALOS_VERSION",
		"cost": "Sidero supports a client within one minor of the cluster. Two apart is where commands start behaving differently from their documentation - which is a bad thing to discover while running `talosctl rotate-ca` during an incident.",
	},
	{
		"client": "kubectl", "client_key": "KUBECTL_VERSION",
		"cluster": "Kubernetes", "cluster_key": "KUBERNETES_VERSION",
		"cost": "Kubernetes supports a client within one minor of the API server. Further apart and commands start failing or behaving differently from their documentation, which is a bad thing to discover while draining a node during an incident.",
	},
]

version_parts(v) := [to_number(m[1]), to_number(m[2])] if {
	some m in regex.find_all_string_submatch_n(`^v?(\d+)\.(\d+)\.\d+$`, v, 1)
}

within_a_minor(a, b) if {
	a[0] == b[0]
	abs(a[1] - b[1]) <= 1
}

deny contains msg if {
	some pair in pairs
	some key in [pair.client_key, pair.cluster_key]
	not version_parts(object.get(pins, key, ""))
	msg := $"could not find {key}=X.Y.Z in scripts/versions.env. If the pin was restructured, this contract needs re-examining rather than re-pointing: something still has to keep {pair.client} in step with the cluster."
}

deny contains msg if {
	some pair in pairs
	client := pins[pair.client_key]
	cluster := pins[pair.cluster_key]
	client_parts := version_parts(client)
	cluster_parts := version_parts(cluster)
	not within_a_minor(client_parts, cluster_parts)
	msg := $"{pair.client} is pinned to {client} and the cluster runs {pair.cluster} {cluster} (scripts/versions.env). {pair.cost}"
}
