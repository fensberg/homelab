package rules.supplier_providers_test

import data.rules.supplier_providers
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
	count(supplier_providers.deny) == 0 with input as good
}

test_an_undeclared_provider_is_refused if {
	denies(supplier_providers.deny, `locks the provider "evilcorp/kubernetes"`) with input as array.concat(good, [tf("evilcorp/kubernetes")])
}

test_an_approved_provider_nobody_requires_is_refused if {
	denies(supplier_providers.deny, `approves the provider "hashicorp/gone"`) with input as swap(0, list({"providers": [{"source": "hashicorp/kubernetes", "reason": "r"}, {"source": "hashicorp/gone", "reason": "r"}]}))
}

test_no_provider_declared_is_refused if {
	denies(supplier_providers.deny, "declares no providers") with input as swap(0, list({"providers": []}))
}

test_no_provider_locked_is_refused if {
	denies(supplier_providers.deny, "no provider is locked by any root or module") with input as [list({})]
}
