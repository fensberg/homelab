# The runner manifests' namespaces agree with OpenTofu's. The estate's other
# runner rules are its names (rules.runner_names), where its pods may land
# (rules.runner_affinity) and what they ask for (rules.runner_requests).
#
# The runner manifests name their namespaces as literals, while OpenTofu
# declares the same values in modules/infrastructure/platform/variables.tf and creates
# those namespaces from them.
#
# The credential secret's name is deliberately absent from this check: it is
# substituted by Flux from the same local, so the two cannot drift by
# construction. Substitution beats an assertion wherever it is available -
# this rule only exists for the values that could not be substituted, because
# a ${VAR} in metadata.name does not survive CI rendering it empty.
#
# Two declarations of one fact, which is only safe if something
# asserts they agree - the same reasoning that has registry.tf and config.go
# implementing the config contract twice.
#
# The alternative was substituting all of them through Flux, as the state
# database does. That was rejected for a boundary reason rather than a design
# one: every substituted variable has to be listed in pr-validation.yml's
# envsubst environment for kubeconform to see a complete manifest, and the
# agent that writes these files deliberately holds no `workflows` permission,
# so it cannot edit that file. Literals plus this test reach the same place
# without needing a permission the boundary is built to withhold.
package rules.runner_namespaces

import data.lib
import rego.v1

releases(name) := [f | some f in lib.objects; f.contents.kind == "HelmRelease"; f.contents.metadata.name == name]

sets := [f | some f in lib.objects; f.contents.kind == "HelmRelease"; f.contents.spec.chart.spec.chart == "gha-runner-scale-set"]

local(name) := {[f.path, v] |
	some f in lib.opentofu
	some block in object.get(f.contents, "locals", [])
	v := block[name]
	is_string(v)
}

# What one manifest file says, in every object of it.
says(path, want) if {
	some f in lib.objects
	f.path == path
	walk(f.contents, [_, v])
	is_string(v)
	contains(v, want)
}

namespaces := {"runner_system_namespace": "gha-runner-scale-set-controller", "runners_namespace": "self-hosted"}

deny contains msg if {
	some name, release in namespaces
	count(local(name)) != 1
	msg := $"local.{name} is declared {count(local(name))} time(s) beside the runner's other names, and it is one fact"
}

deny contains msg if {
	some name, release in namespaces
	count(releases(release)) != 1
	msg := $"{count(releases(release))} HelmRelease named {release} were found, so its namespace cannot be read"
}

deny contains msg if {
	some name, release in namespaces
	some [tf, want] in local(name)
	some f in releases(release)
	not says(f.path, want)
	msg := $"{tf} declares local.{name} = \"{want}\", but {f.path} never mentions it. The manifest and the OpenTofu have drifted; one of them is now describing a resource the other does not create."
}
