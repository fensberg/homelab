package rules.runner_images_test

import data.rules.runner_images
import rego.v1

set(name, image) := {"path": "clusters/r.yaml", "contents": {"kind": "HelmRelease", "metadata": {"name": name}, "spec": {"chart": {"spec": {"chart": "gha-runner-scale-set"}}, "values": {"template": {"spec": {"containers": [{"name": "runner", "image": image}]}}}}}}

pinned := "ghcr.io/o/runner:1@sha256:aaaa"

test_pinned_sets_pass if {
	count(runner_images.deny) == 0 with input as [set("self-hosted", pinned), set("pr-builds", pinned)]
}

test_an_image_on_a_tag_is_refused if {
	some msg in runner_images.deny with input as [set("self-hosted", pinned), set("pr-builds", "ghcr.io/o/runner:latest")]
	contains(msg, `"ghcr.io/o/runner:latest" is not pinned by digest`)
}

test_a_set_with_no_image_is_refused if {
	bare := {"path": "clusters/r.yaml", "contents": {"kind": "HelmRelease", "metadata": {"name": "self-hosted"}, "spec": {"chart": {"spec": {"chart": "gha-runner-scale-set"}}, "values": {}}}}
	some msg in runner_images.deny with input as [bare]
	contains(msg, "names no image")
}

test_the_converges_set_missing_is_refused if {
	some msg in runner_images.deny with input as [set("pr-builds", pinned)]
	contains(msg, "(self-hosted) is not among them")
}
