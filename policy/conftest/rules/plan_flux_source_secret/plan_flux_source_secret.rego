# A Flux source may only name a secret something in this repository creates.
#
# WHAT THIS GUARDS. `secretRef` on a GitRepository is optional, and a public
# repository needs none - the `flux-system` source has never had one and has
# always worked. But a secretRef naming a secret that does not exist does not
# degrade to anonymous access. It fails closed, with AuthenticationFailed, and
# the source never produces an artifact.
#
# That is worse than it sounds, because of what sits downstream. The Kustomization
# reading that source reports "Source artifact not found", which reads as a
# missing tag rather than as a missing secret. And the layer that applies the
# source has wait:true, so it waits for its own GitRepository to go Ready,
# never gets there, and eventually times out - taking the ignition with it,
# since the health gate is upstream of the point of no return.
#
# One line naming a secret that was never created cost a full rebuild:
#
#   flux-production   False   failed to get secret 'flux-system/flux-system':
#                             secrets "flux-system" not found
#
# The secret was assumed to exist because `flux bootstrap` creates one - but
# Flux is installed here by OpenTofu applying the manifests directly, which
# creates no such secret. An assumption about another tool's side effects,
# written as a reference.
# SECRETS ARE READ OFF THE PLAN: each a plan of a site's roots would make,
# by the name OpenTofu works out for it.
package rules.plan_flux_source_secret

import data.lib
import rego.v1

# The Flux tree, without Flux's own definitions, which are vendored verbatim.
manifests contains f if {
	some f in lib.under("clusters/")
	not endswith(f.path, "gotk-components.yaml")
	is_object(f.contents)
}

named contains [f.path, ref.name] if {
	some f in manifests
	walk(f.contents, [path, ref])
	count(path) > 0
	path[count(path) - 1] == "secretRef"
	is_string(ref.name)
}

# created is the name of every secret a plan of a site's roots would make,
# each instance of each, as OpenTofu works it out: a secret named in a loop
# is read by its name too.
created contains name if {
	some [_, r] in lib.plan
	r.type == "kubernetes_secret"
	some m in object.get(r.values, "metadata", [])
	is_string(m.name)
	name := m.name
}

deny contains msg if {
	some [path, name] in named
	not name in created
	msg := $"{path} names secretRef \"{name}\", which nothing in this repository creates. A Flux source with a secretRef pointing at a missing secret does not fall back to anonymous access - it fails with AuthenticationFailed and never produces an artifact. If the repository is public, delete the secretRef. If it is not, create the secret where it is needed and name it here. Secrets this repository creates: {sort(created)}"
}

deny contains msg if {
	count(created) == 0
	msg := "no kubernetes_secret was found in any plan, so every secret a Flux source names would look uncreated - or, with none named, this would pass having checked nothing"
}

deny contains msg if {
	files := {f.path | some f in manifests}
	count(files) < 10
	msg := $"only {count(files)} manifest(s) were read under clusters/, expected at least 10 - the reader has stopped finding them, so this rule proves nothing"
}
