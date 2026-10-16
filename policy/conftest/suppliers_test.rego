package rules.suppliers_test

import data.rules.suppliers
import rego.v1

list(over) := {"path": "scripts/approved-suppliers.yml", "contents": object.union({
	"registries": [{"host": "ghcr.io", "reason": "the estate's own"}],
	"charts": [{"url": "https://charts.example.org", "reason": "a chart"}],
	"endpoints": [{"group": "github", "reason": "source", "hosts": ["github.com:443"]}],
	"exemptions": [],
	"hooks": [{"repo": "https://github.com/pre-commit/pre-commit-hooks", "reason": "yaml checks"}],
	"providers": [{"source": "hashicorp/kubernetes", "reason": "the platform"}],
}, over)}

own := {"path": "clusters/core/flux-system/gotk-sync.yaml", "contents": {"kind": "GitRepository", "metadata": {"name": "flux-system"}, "spec": {"url": "https://github.com/owner/name"}}}

deployment(image) := {"path": "clusters/core/app/deploy.yaml", "contents": {"kind": "Deployment", "spec": {"template": {"spec": {"containers": [{"image": image}]}}}}}

pinned := "ghcr.io/owner/thing:1@sha256:aaaa"

source(u) := {"path": "clusters/core/app/source.yaml", "contents": {"kind": "HelmRepository", "spec": {"url": u}}}

workflow(hosts) := {"path": ".github/workflows/w.yml", "contents": {"jobs": {"j": {"steps": [{"with": {"allowed-endpoints": hosts}}]}}}}

hooks(repos) := {"path": ".pre-commit-config.yaml", "contents": {"repos": repos}}

hook := {"repo": "https://github.com/pre-commit/pre-commit-hooks", "rev": "v6.0.0"}

tf(source) := {"path": "management/p/versions.tf", "contents": {"terraform": [{"required_providers": [{"kubernetes": {"source": source}}]}], "module": {"m": [{"source": "./parts"}]}}}

good := [list({}), own, deployment(pinned), source("https://charts.example.org"), workflow("github.com:443\n"), hooks([hook, {"repo": "local"}]), tf("hashicorp/kubernetes")]

swap(i, f) := array.concat(array.concat(array.slice(good, 0, i), [f]), array.slice(good, i + 1, count(good)))

denies(i, words) if {
	some msg in suppliers.deny with input as i
	contains(msg, words)
}

test_everything_declared_and_pinned_passes if {
	count(suppliers.deny) == 0 with input as good
}

test_an_entry_with_no_reason_is_refused if {
	denies(swap(0, list({"registries": [{"host": "ghcr.io", "reason": " "}]})), `registry "ghcr.io" is approved with no reason given`)
}

test_an_exemption_with_no_trigger_or_issue_is_refused if {
	e := list({"exemptions": [{"path": "clusters/core/app/deploy.yaml", "rule": "digest-pinned-images", "reason": "r"}]})
	i := swap(2, deployment("ghcr.io/owner/thing:1"))
	denies(array.concat([e], array.slice(i, 1, count(i))), "gives no trigger")
	denies(array.concat([e], array.slice(i, 1, count(i))), "names no issue")
}

test_an_unapproved_registry_is_refused if {
	denies(swap(2, deployment("docker.io/library/thing@sha256:aaaa")), `comes from "docker.io", which is not an approved supplier`)
}

test_a_tagged_image_is_refused if {
	denies(swap(2, deployment("ghcr.io/owner/thing:1")), "is not pinned by digest")
}

test_an_image_in_a_block_of_yaml_is_read_too if {
	cm := {"path": "clusters/core/app/values.yaml", "contents": {"kind": "ConfigMap", "data": {"values.yaml": "worker:\n  image: ghcr.io/owner/other:2\n"}}}
	denies(array.concat(good, [cm]), `image "ghcr.io/owner/other:2" is not pinned by digest`)
}

test_an_overlay_beside_it_pins_a_tag if {
	k := {"path": "clusters/core/app/kustomization.yaml", "contents": {"images": [{"name": "ghcr.io/owner/thing", "digest": "sha256:aaaa"}]}}
	count(suppliers.deny) == 0 with input as array.concat(swap(2, deployment("ghcr.io/owner/thing:1")), [k])
}

test_a_stale_exemption_is_refused if {
	e := list({"exemptions": [{"path": "clusters/core/app/deploy.yaml", "rule": "digest-pinned-images", "reason": "r", "trigger": "t", "issue": 1}]})
	denies(swap(0, e), "is exempted from digest pinning but no longer needs to be")
}

test_an_unapproved_chart_source_is_refused if {
	denies(swap(3, source("oci://registry.example.net/charts")), "is not an approved supplier. Add it")
}

test_the_repositorys_own_and_its_releases_are_not_suppliers if {
	i := array.concat(good, [source("https://github.com/owner/name"), source("oci://${RELEASE_REPOSITORY}-thing-release")])
	count(suppliers.deny) == 0 with input as i
}

test_an_undeclared_egress_host_is_refused if {
	denies(swap(4, workflow("github.com:443\nevil.example:443\n")), `reaches "evil.example:443"`)
}

test_an_undeclared_hook_is_refused if {
	denies(swap(5, hooks([hook, {"repo": "https://github.com/example/undeclared", "rev": "v1"}])), "approved-suppliers.yml does not declare it")
}

test_a_declared_hook_nobody_uses_is_refused if {
	denies(swap(5, hooks([{"repo": "local"}])), "does not use it")
}

test_a_moving_rev_is_refused if {
	denies(swap(5, hooks([object.union(hook, {"rev": "main"})])), `is pinned to "main", which moves`)
	denies(swap(5, hooks([object.remove(hook, ["rev"])])), `is pinned to "", which moves`)
}

test_an_undeclared_provider_is_refused if {
	denies(array.concat(good, [tf("evilcorp/kubernetes")]), `requires the provider "evilcorp/kubernetes"`)
}

test_an_approved_provider_nobody_requires_is_refused if {
	denies(swap(0, list({"providers": [{"source": "hashicorp/kubernetes", "reason": "r"}, {"source": "hashicorp/gone", "reason": "r"}]})), `approves the provider "hashicorp/gone"`)
}
