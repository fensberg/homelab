# Each client the estate pins is within one minor of the cluster it talks to:
# talosctl of Talos, and kubectl of Kubernetes.
#
# talosctl is a client for a specific Talos version. Sidero's own guidance is
# to keep it within one minor of the cluster it talks to, and the two are
# pinned in different files by different people at different times - the exact
# shape of drift that goes unnoticed until a command behaves oddly during an
# incident.
#
# This is another gotcha turned into a rule: it was found by reading
# install-dependencies.sh while writing a rotation runbook, not by anything
# failing, which is precisely why it deserves a check rather than a note.
package rules.client_skew

import data.lib
import rego.v1

pins := [f.contents | some f in lib.at("scripts/versions.env")][0]

# What the cluster's module says a site runs, as a local.
declared(key) := {[f.path, v] |
	some f in lib.opentofu
	some block in object.get(f.contents, "locals", [])
	v := block[key]
	is_string(v)
}

version_parts(v) := [to_number(m[1]), to_number(m[2]), m[3]] if {
	some m in regex.find_all_string_submatch_n(`^v?(\d+)\.(\d+)\.(\d+)$`, v, 1)
}

within_a_minor(a, b) if {
	a[0] == b[0]
	abs(a[1] - b[1]) <= 1
}

# kubectl is a client for a specific Kubernetes version, and the project
# supports it within one minor of the cluster in either direction. Same shape
# as the talosctl contract above, same reason: the two numbers live in
# different files and drift without anything failing.
#
# It had drifted, in both directions at once. The cluster ran 1.31.1 pinned
# inline in talos.tf where nothing watched it, versions.env said 1.34.11, and
# the workstation installer ignored the pin and fetched whatever upstream
# called stable - three numbers, no two of them within a minor of each other.

deny contains msg if {
	not version_parts(object.get(pins, "KUBECTL_VERSION", ""))
	msg := "could not find KUBECTL_VERSION=vX.Y.Z in scripts/versions.env. If the pin was restructured, this contract needs re-examining rather than re-pointing: something still has to keep the client in step with the cluster."
}

deny contains msg if {
	not version_parts(object.get(pins, "TALOSCTL_VERSION", ""))
	msg := "could not find TALOSCTL_VERSION=vX.Y.Z in scripts/versions.env. If the pin was restructured, this contract needs re-examining rather than re-pointing: something still has to keep the client in step with the cluster."
}

deny contains msg if {
	count(declared("talos_version")) != 1
	msg := $"talos_version is declared as a version {count(declared("talos_version"))} time(s) where the cluster's module says what a site runs, and it is one fact"
}

deny contains msg if {
	cli := version_parts(pins.TALOSCTL_VERSION)
	some [path, v] in declared("talos_version")
	cluster := version_parts(v)
	not within_a_minor(cli, cluster)
	msg := sprintf("talosctl is pinned to v%d.%d.%s but the cluster runs v%d.%d.%s (%s). Sidero supports a client within one minor of the cluster. Two apart is where commands start behaving differently from their documentation - which is a bad thing to discover while running `talosctl rotate-ca` during an incident.", array.flatten([cli, cluster, [path]]))
}

deny contains msg if {
	count(declared("kubernetes_version")) != 1
	msg := $"kubernetes_version is declared as a version {count(declared("kubernetes_version"))} time(s) where the cluster's module says what a site runs. It was inline in talos.tf once, which is how it went five minors stale without anything noticing. It belongs in the locals beside talos_version, because Talos decides which Kubernetes versions are installable at all."
}

deny contains msg if {
	cli := version_parts(pins.KUBECTL_VERSION)
	some [path, v] in declared("kubernetes_version")
	cluster := version_parts(v)
	not within_a_minor(cli, cluster)
	msg := sprintf("kubectl is pinned to v%d.%d.%s but the cluster runs %d.%d.%s (%s). Kubernetes supports a client within one minor of the API server. Further apart and commands start failing or behaving differently from their documentation, which is a bad thing to discover while draining a node during an incident.", array.flatten([cli, cluster, [path]]))
}
