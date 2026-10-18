# Every Flux controller the generated manifest ships is pinned by this overlay.
#
# `gotk-components.yaml` is written wholesale by `flux bootstrap` and pins its
# controllers by TAG. The kustomization beside it replaces each tag with a
# digest, and the transformer matches on image NAME - so a controller the
# overlay does not name keeps its tag and runs unpinned.
#
# `task render-flux-digests` now reads the tags out of the generated manifest
# rather than a list, so a new controller is picked up automatically (#104).
# This is the half that fails when it is not: a controller added upstream, or
# one renamed, goes from pinned to mutable with the manifest still building and
# every other check still green.
package rules.flux_controllers

import data.lib
import rego.v1

components := "gotk-components.yaml"

dir(path) := concat("/", array.slice(parts, 0, count(parts) - 1)) if parts := split(path, "/")

shipped contains [dir(f.path), m[1]] if {
	some f in lib.named(components)
	walk(f.contents, [path, v])
	count(path) > 0
	path[count(path) - 1] == "image"
	is_string(v)
	some m in regex.find_all_string_submatch_n(`^ghcr\.io/fluxcd/([a-z-]+):v[0-9.]+`, v, 1)
}

pinned(at, name) if {
	some f in lib.at(sprintf("%s/kustomization.yaml", [at]))
	some image in object.get(f.contents, "images", [])
	image.name == sprintf("ghcr.io/fluxcd/%s", [name])
	contains(object.get(image, "digest", ""), "sha256:")
}

deny contains msg if {
	count({name | some [_, name] in shipped}) < 4
	msg := sprintf("found %d controller image(s) in %s, and Flux ships at least four. The parse has stopped matching, so a controller running unpinned would be invisible to this - which is the same green as everything being pinned.", [count({name | some [_, name] in shipped}), components])
}

deny contains msg if {
	some [at, name] in shipped
	not pinned(at, name)
	msg := sprintf("%s ships ghcr.io/fluxcd/%s and the kustomization beside it does not pin it. kustomize's image transformer matches on NAME, so a controller it does not name keeps the mutable tag the generated manifest carries - on the component that applies everything else in the cluster. The overlay still builds and nothing else objects. Run: task render-flux-digests", [components, name])
}
