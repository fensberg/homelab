package rules.supplier_reasons_test

import data.rules.supplier_reasons
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
	count(supplier_reasons.deny) == 0 with input as good
}

test_an_entry_with_no_reason_is_refused if {
	denies(supplier_reasons.deny, `registry "ghcr.io" is approved with no reason given`) with input as swap(0, list({"registries": [{"host": "ghcr.io", "reason": " "}]}))
}

test_an_exemption_with_no_trigger_or_issue_is_refused if {
	e := list({"exemptions": [{"path": "clusters/core/app/deploy.yaml", "rule": "digest-pinned-images", "reason": "r"}]})
	i := swap(2, deployment("ghcr.io/owner/thing:1"))
	denies(supplier_reasons.deny, "gives no trigger") with input as array.concat([e], array.slice(i, 1, count(i)))
	denies(supplier_reasons.deny, "names no issue") with input as array.concat([e], array.slice(i, 1, count(i)))
}

test_no_registry_or_endpoint_is_refused if {
	empty := list({"registries": [], "endpoints": []})
	denies(supplier_reasons.deny, "declares no registries") with input as [empty]
	denies(supplier_reasons.deny, "declares no endpoints") with input as [empty]
}

test_every_kind_of_entry_owes_a_reason if {
	silent := list({
		"charts": [{"url": "https://charts.example.org", "reason": ""}],
		"endpoints": [{"group": "github", "reason": " ", "hosts": ["github.com:443"]}],
		"exemptions": [{"path": "clusters/x.yaml", "trigger": "t", "issue": 1, "reason": ""}],
		"providers": [{"source": "hashicorp/kubernetes", "reason": ""}],
	})
	denies(supplier_reasons.deny, `chart source "https://charts.example.org" is approved with no reason given`) with input as [silent]
	denies(supplier_reasons.deny, `endpoint group "github" is approved with no reason given`) with input as [silent]
	denies(supplier_reasons.deny, `exemption for "clusters/x.yaml" gives no reason`) with input as [silent]
	denies(supplier_reasons.deny, `provider "hashicorp/kubernetes" is approved with no reason given`) with input as [silent]
}
