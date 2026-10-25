package rules.supplier_charts_test

import data.rules.supplier_charts
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
	count(supplier_charts.deny) == 0 with input as good
}

test_an_unapproved_chart_source_is_refused if {
	denies(supplier_charts.deny, "is not an approved supplier. Add it") with input as swap(3, source("oci://registry.example.net/charts"))
}

test_the_repositorys_own_and_its_releases_are_not_suppliers if {
	i := array.concat(good, [source("https://github.com/owner/name"), source("oci://${RELEASE_REPOSITORY}-thing-release")])
	count(supplier_charts.deny) == 0 with input as i
}

test_no_repository_of_its_own_is_refused if {
	denies(supplier_charts.deny, "names no repository on GitHub") with input as swap(1, {"path": "clusters/core/flux-system/gotk-sync.yaml", "contents": {"kind": "GitRepository", "metadata": {"name": "flux-system"}, "spec": {"url": "ssh://elsewhere/x"}}})
}

test_no_chart_source_is_refused if {
	denies(supplier_charts.deny, "no chart sources found under clusters/") with input as [list({}), own]
}
