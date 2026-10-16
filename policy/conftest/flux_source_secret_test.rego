package flux_source_secret_test

import data.flux_source_secret
import rego.v1

secret(name) := {"path": "modules/p/secrets.tf", "contents": {"resource": {"kubernetes_secret": {"s": [{"metadata": [{"name": name, "namespace": "flux-system"}]}]}}}}

filler := [{"path": sprintf("clusters/core/f%d.yaml", [i]), "contents": {"kind": "ConfigMap"}} | some i in numbers.range(1, 10)]

source(ref) := {"path": "clusters/core/s.yaml", "contents": {"kind": "OCIRepository", "spec": {"secretRef": ref}}}

test_a_created_secret_passes if {
	count(flux_source_secret.deny) == 0 with input as array.concat(filler, [secret("registry"), source({"name": "registry"})])
}

test_a_secret_nothing_creates_is_refused if {
	some msg in flux_source_secret.deny with input as array.concat(filler, [secret("registry"), source({"name": "flux-system"})])
	contains(msg, `names secretRef "flux-system", which nothing in this repository creates`)
}

test_a_secret_named_deep_in_a_pod_is_read_too if {
	pod := {"path": "clusters/core/d.yaml", "contents": {"kind": "Deployment", "spec": {"template": {"spec": {"containers": [{"envFrom": [{"secretRef": {"name": "missing"}}]}]}}}}}
	some msg in flux_source_secret.deny with input as array.concat(filler, [secret("registry"), pod])
	contains(msg, `"missing"`)
}

test_flux_own_definitions_are_not_read if {
	vendored := {"path": "clusters/core/flux-system/gotk-components.yaml", "contents": {"spec": {"secretRef": {"name": "anything"}}}}
	count(flux_source_secret.deny) == 0 with input as array.concat(filler, [secret("registry"), vendored])
}

test_no_secret_created_is_refused if {
	some msg in flux_source_secret.deny with input as filler
	contains(msg, "no kubernetes_secret was found")
}

test_too_few_manifests_is_refused if {
	some msg in flux_source_secret.deny with input as [secret("registry")]
	contains(msg, "only 0 manifest(s) were read")
}
