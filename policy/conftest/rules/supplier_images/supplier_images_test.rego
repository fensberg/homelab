package rules.supplier_images_test

import data.rules.supplier_images
import rego.v1

list(over) := {"path": "scripts/approved-suppliers.yml", "contents": object.union(
	{
		"registries": [{"host": "ghcr.io", "reason": "the estate's own"}],
		"charts": [{"url": "https://charts.example.org", "reason": "a chart"}],
		"endpoints": [{"group": "github", "reason": "source", "hosts": ["github.com:443"]}],
		"exemptions": [],
		"hooks": [{"repo": "https://github.com/pre-commit/pre-commit-hooks", "reason": "yaml checks"}],
		"providers": [{"source": "hashicorp/kubernetes", "reason": "the platform"}],
	},
	over,
)}

own := {"path": "clusters/core/flux-system/gotk-sync.yaml", "contents": {"kind": "GitRepository", "metadata": {"name": "flux-system"}, "spec": {"url": "https://github.com/owner/name"}}}

deployment(image) := {"path": "clusters/core/app/deploy.yaml", "contents": {"kind": "Deployment", "spec": {"template": {"spec": {"containers": [{"image": image}]}}}}}

pinned := "ghcr.io/owner/thing:1@sha256:aaaa"

source(u) := {"path": "clusters/core/app/source.yaml", "contents": {"kind": "HelmRepository", "spec": {"url": u}}}

workflow(hosts) := {"path": ".github/workflows/w.yml", "contents": {"jobs": {"j": {"steps": [{"with": {"allowed-endpoints": hosts}}]}}}}

hooks(repos) := {"path": ".pre-commit-config.yaml", "contents": {"repos": repos}}

hook := {"repo": "https://github.com/pre-commit/pre-commit-hooks", "rev": "v6.0.0"}

# The key is made first: OPA 1.20.2 cannot take a template string written as
# an object's key, and stops.
tf(source) := {"path": "management/p/.terraform.lock.hcl", "contents": {"provider": {key: [{"version": "2.38.0", "hashes": ["h1:x"]}]}}} if {
	key := $"registry.opentofu.org/{source}"
}

good := [list({}), own, deployment(pinned), source("https://charts.example.org"), workflow("github.com:443\n"), hooks([hook, {"repo": "local"}]), tf("hashicorp/kubernetes")]

swap(i, f) := array.flatten([array.slice(good, 0, i), [f], array.slice(good, i + 1, count(good))])

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_everything_declared_and_pinned_passes if {
	count(supplier_images.deny) == 0 with input as good
}

test_an_unapproved_registry_is_refused if {
	denies(supplier_images.deny, `comes from "docker.io", which is not an approved supplier`) with input as swap(2, deployment("docker.io/library/thing@sha256:aaaa"))
}

test_a_tagged_image_is_refused if {
	denies(supplier_images.deny, "is not pinned by digest") with input as swap(2, deployment("ghcr.io/owner/thing:1"))
}

test_an_image_in_a_block_of_yaml_is_read_too if {
	cm := {"path": "clusters/core/app/values.yaml", "contents": {"kind": "ConfigMap", "data": {"values.yaml": "worker:\n  image: ghcr.io/owner/other:2\n"}}}
	denies(supplier_images.deny, `image "ghcr.io/owner/other:2" is not pinned by digest`) with input as array.concat(good, [cm])
}

test_an_overlay_beside_it_pins_a_tag if {
	k := {"path": "clusters/core/app/kustomization.yaml", "contents": {"images": [{"name": "ghcr.io/owner/thing", "digest": "sha256:aaaa"}]}}
	count(supplier_images.deny) == 0 with input as array.concat(swap(2, deployment("ghcr.io/owner/thing:1")), [k])
}

test_a_stale_exemption_is_refused if {
	e := list({"exemptions": [{"path": "clusters/core/app/deploy.yaml", "rule": "digest-pinned-images", "reason": "r", "trigger": "t", "issue": 1}]})
	denies(supplier_images.deny, "is exempted from digest pinning but no longer needs to be") with input as swap(0, e)
}

test_no_image_reference_is_refused if {
	denies(supplier_images.deny, "no image references found under clusters/") with input as [list({})]
}
