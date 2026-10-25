package rules.supplier_hooks_test

import data.rules.supplier_hooks
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
	count(supplier_hooks.deny) == 0 with input as good
}

test_an_undeclared_hook_is_refused if {
	denies(supplier_hooks.deny, "approved-suppliers.yml does not declare it") with input as swap(5, hooks([hook, {"repo": "https://github.com/example/undeclared", "rev": "v1"}]))
}

test_a_declared_hook_nobody_uses_is_refused if {
	denies(supplier_hooks.deny, "does not use it") with input as swap(5, hooks([{"repo": "local"}]))
}

test_a_moving_rev_is_refused if {
	denies(supplier_hooks.deny, `is pinned to "main", which moves`) with input as swap(5, hooks([object.union(hook, {"rev": "main"})]))
	denies(supplier_hooks.deny, `is pinned to "", which moves`) with input as swap(5, hooks([object.remove(hook, ["rev"])]))
}

test_a_hook_declared_with_no_reason_is_refused if {
	silent := list({"hooks": [{"repo": "https://github.com/pre-commit/pre-commit-hooks", "reason": " "}]})
	denies(supplier_hooks.deny, "is declared with no reason") with input as swap(0, silent)
}
