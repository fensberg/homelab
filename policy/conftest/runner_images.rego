# Every runner scale set pins its image by digest.
#
# Without an image the scale set silently falls back to the chart's default
# runner, which carries none of the toolchain the program shells out to. And
# a tag can be moved by anyone who can push to the registry, so the estate
# would be converged by whatever that tag resolves to on the day - a change
# nothing in this repository would record or review.
package runner_images

import rego.v1

sets contains f if {
	some f in input
	f.contents.kind == "HelmRelease"
	object.get(f.contents, ["spec", "chart", "spec", "chart"], "") == "gha-runner-scale-set"
}

images(f) := {v |
	walk(f.contents.spec.values, [path, v])
	count(path) > 0
	path[count(path) - 1] == "image"
	is_string(v)
}

deny contains msg if {
	not "self-hosted" in {f.contents.metadata.name | some f in sets}
	msg := sprintf("the runner scale sets found are %v, and the one every site's converge runs on (self-hosted) is not among them", [sort({f.contents.metadata.name | some f in sets})])
}

deny contains msg if {
	some f in sets
	count(images(f)) == 0
	msg := sprintf("%s: the runner scale set %s names no image. Without one the scale set silently falls back to the chart's default runner, which carries none of the toolchain the program shells out to - a converge would fail at Render with \"1Password CLI not found on PATH\".", [f.path, f.contents.metadata.name])
}

deny contains msg if {
	some f in sets
	some image in images(f)
	not contains(image, "@sha256:")
	msg := sprintf("%s: the runner scale set image %q is not pinned by digest. A tag can be moved by anyone who can push to the registry, so the estate would be converged by whatever that tag resolves to on the day - a change nothing in this repository would record or review. Use image@sha256:...; :latest is for humans reading the registry.", [f.path, image])
}
