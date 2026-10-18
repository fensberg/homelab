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
package rules.flux_source_secret

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

created contains name if {
	some f in lib.opentofu
	some _, blocks in object.get(object.get(f.contents, "resource", {}), "kubernetes_secret", {})
	some block in blocks
	some m in block.metadata
	is_string(m.name)
	name := m.name
}

deny contains msg if {
	some [path, name] in named
	not name in created
	msg := sprintf("%s names secretRef %q, which nothing in this repository creates. A Flux source with a secretRef pointing at a missing secret does not fall back to anonymous access - it fails with AuthenticationFailed and never produces an artifact. If the repository is public, delete the secretRef. If it is not, create the secret where it is needed and name it here. Secrets this repository creates: %v", [path, name, sort(created)])
}

deny contains msg if {
	count(created) == 0
	msg := "no kubernetes_secret was found in any OpenTofu file, so every secret a Flux source names would look uncreated - or, with none named, this would pass having checked nothing"
}

deny contains msg if {
	files := {f.path | some f in manifests}
	count(files) < 10
	msg := sprintf("only %d manifest(s) were read under clusters/, expected at least 10 - the reader has stopped finding them, so this rule proves nothing", [count(files)])
}
