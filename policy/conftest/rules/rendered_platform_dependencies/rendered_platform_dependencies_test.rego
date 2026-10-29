package rules.rendered_platform_dependencies_test

import data.rules.rendered_platform_dependencies
import rego.v1

layer(docs) := {"path": "/tmp/r/layer.json", "contents": {
	"layer": {"file": "clusters/core/layers.yaml", "name": "configs", "in_namespace": "flux-system", "path": "clusters/core/configs", "substitute_from": [{"kind": "Secret", "name": "vars"}]},
	"documents": docs,
}}

tofu(type, ns, name, extra) := {"address": $"{type}.{name}", "mode": "managed", "type": type, "values": object.union({"metadata": [{"name": name, "namespace": ns}]}, extra)}

vars := tofu("kubernetes_secret", "flux-system", "vars", {"data": {"DB_NS": "database"}})

plan(scope, resources) := {"path": $"/tmp/plans/{scope}/site0-platform.plan.json", "contents": {"planned_values": {"root_module": {"resources": resources}}}}

both(resources) := [plan("site", resources), plan("pinned", resources)]

made := [vars, tofu("kubernetes_namespace", "", "apps", {}), tofu("kubernetes_namespace", "", "database", {}), tofu("kubernetes_service_account", "apps", "runs-it", {})]

deployment(ns, account) := {"kind": "Deployment", "metadata": {"name": "d", "namespace": ns}, "spec": {"template": {"spec": {"serviceAccountName": account}}}}

docs := [
	deployment("apps", "runs-it"),
	{"kind": "Service", "metadata": {"name": "db", "namespace": "${DB_NS}"}},
	{"kind": "Namespace", "metadata": {"name": "own"}},
	{"kind": "ServiceAccount", "metadata": {"name": "own-account", "namespace": "own"}},
	{"kind": "CronJob", "metadata": {"name": "c", "namespace": "own"}, "spec": {"jobTemplate": {"spec": {"template": {"spec": {"serviceAccountName": "own-account"}}}}}},
]

good := array.concat([layer(docs)], both(made))

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_what_every_plan_or_the_manifests_make_passes if {
	count(rendered_platform_dependencies.deny) == 0 with input as good
}

test_a_namespace_only_the_checkout_makes_is_refused if {
	ahead := [layer(docs), plan("site", made), plan("pinned", [vars, tofu("kubernetes_namespace", "", "database", {}), tofu("kubernetes_service_account", "apps", "runs-it", {})])]
	denies(rendered_platform_dependencies.deny, "needs the Namespace apps, which only the checkout's modules make") with input as ahead
}

test_an_account_the_checkout_no_longer_makes_is_refused if {
	behind := [layer(docs), plan("site", [vars, tofu("kubernetes_namespace", "", "apps", {}), tofu("kubernetes_namespace", "", "database", {})]), plan("pinned", made)]
	denies(rendered_platform_dependencies.deny, "needs the ServiceAccount apps/runs-it, which the release a site runs makes and the checkout's modules no longer do") with input as behind
}

test_what_nothing_makes_is_refused if {
	values_from := {"kind": "HelmRelease", "metadata": {"name": "h", "namespace": "apps"}, "spec": {"valuesFrom": [{"kind": "Secret", "name": "missing"}], "targetNamespace": "nowhere"}}
	nothing := array.concat([layer(array.concat(docs, [values_from]))], both(made))
	denies(rendered_platform_dependencies.deny, "needs the Secret apps/missing, and nothing makes it") with input as nothing
	denies(rendered_platform_dependencies.deny, "needs the Namespace nowhere, and nothing makes it") with input as nothing
}

test_a_namespace_a_release_creates_itself_is_not_needed if {
	creates := {"kind": "HelmRelease", "metadata": {"name": "h", "namespace": "apps"}, "spec": {"targetNamespace": "its-own", "install": {"createNamespace": true}}}
	count(rendered_platform_dependencies.deny) == 0 with input as array.concat([layer(array.concat(docs, [creates]))], both(made))
}

test_a_name_no_secret_gives_is_left_to_the_variables_rule if {
	unknown := {"kind": "Service", "metadata": {"name": "x", "namespace": "${NOBODY}"}}
	count(rendered_platform_dependencies.deny) == 0 with input as array.concat([layer(array.concat(docs, [unknown]))], both(made))
}

test_nothing_to_read_is_refused if {
	denies(rendered_platform_dependencies.deny, "nothing Flux applies was read as needing anything") with input as []
	denies(rendered_platform_dependencies.deny, "no site's plan was made with the release it runs") with input as array.concat([layer(docs)], [plan("site", made)])
}
