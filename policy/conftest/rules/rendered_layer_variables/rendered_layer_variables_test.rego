package rules.rendered_layer_variables_test

import data.rules.rendered_layer_variables
import rego.v1

layer(name, from, docs) := {"path": $"/tmp/r/{name}.json", "contents": {
	"layer": {"file": "clusters/core/layers.yaml", "name": name, "in_namespace": "flux-system", "path": $"clusters/core/{name}", "substitute": {"INLINE": "x"}, "substitute_from": from},
	"documents": docs,
}}

secret(name, keys) := {"address": $"kubernetes_secret.{name}", "mode": "managed", "type": "kubernetes_secret", "values": {"metadata": [{"name": name, "namespace": "flux-system"}], "data": keys}}

planned(resources) := {"planned_values": {"root_module": {"resources": resources}}}

plan("site", resources) := {"path": "/tmp/plans/site/site0-platform.plan.json", "contents": planned(resources)}

plan("pinned", resources) := {"path": "/tmp/plans/pinned/site0-platform.plan.json", "contents": {"pinned_plan": planned(resources)}}

# The checkout's plan and the one made with the release the site runs, alike.
both(resources) := [plan("site", resources), plan("pinned", resources)]

uses(names) := [{"kind": "ConfigMap", "data": {n: $"$\{{n}}" | some n in names}}]

from_vars := [{"kind": "Secret", "name": "vars"}]

vars := both([secret("vars", {"SITE": "x", "ADDRESS_ONE": "y"})])

good := array.concat([layer("configs", from_vars, uses(["SITE", "ADDRESS_ONE", "INLINE"]))], vars)

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_every_variable_given_passes if {
	count(rendered_layer_variables.deny) == 0 with input as good
}

test_a_variable_no_secret_of_the_layer_carries_is_refused if {
	denies(rendered_layer_variables.deny, "the layer configs names ${ELSEWHERE}") with input as array.concat([layer("configs", from_vars, uses(["ELSEWHERE"]))], vars)
}

test_a_key_only_the_checkout_writes_is_refused if {
	ahead := [
		layer("configs", from_vars, uses(["SITE", "NEW_KEY"])),
		plan("site", [secret("vars", {"SITE": "x", "NEW_KEY": "y"})]),
		plan("pinned", [secret("vars", {"SITE": "x"})]),
	]
	denies(rendered_layer_variables.deny, "names ${NEW_KEY} in what it applies (clusters/core/configs), and only the checkout's modules write it") with input as ahead
}

test_a_key_the_checkout_no_longer_writes_is_refused if {
	behind := [
		layer("configs", from_vars, uses(["SITE", "OLD_KEY"])),
		plan("site", [secret("vars", {"SITE": "x"})]),
		plan("pinned", [secret("vars", {"SITE": "x", "OLD_KEY": "y"})]),
	]
	denies(rendered_layer_variables.deny, "which the release a site runs writes and the checkout's modules no longer do") with input as behind
}

test_a_secret_only_the_checkout_writes_is_refused if {
	ahead := [
		layer("configs", from_vars, uses(["SITE"])),
		plan("site", [secret("vars", {"SITE": "x"})]),
		plan("pinned", [secret("other", {"SITE": "x"})]),
	]
	denies(rendered_layer_variables.deny, "which only the checkout's modules write, not the release a site runs") with input as ahead
}

test_no_plan_with_the_release_a_site_runs_is_refused if {
	denies(rendered_layer_variables.deny, "no site's plan was made with the release it runs") with input as [layer("configs", from_vars, uses(["SITE"])), plan("site", [secret("vars", {"SITE": "x"})])]
}

test_a_secret_in_another_namespace_gives_nothing if {
	elsewhere := both([{"address": "kubernetes_secret.vars", "mode": "managed", "type": "kubernetes_secret", "values": {"metadata": [{"name": "vars", "namespace": "other"}], "data": {"SITE": "x"}}}])
	denies(rendered_layer_variables.deny, "which no site's plan writes") with input as array.concat([layer("configs", from_vars, uses(["SITE"]))], elsewhere)
}

test_a_secret_whose_keys_cannot_be_read_is_refused if {
	denies(rendered_layer_variables.deny, "whose keys a plan cannot show") with input as array.concat([layer("configs", from_vars, uses(["SITE"]))], both([secret("vars", null)]))
}

test_a_config_map_source_is_refused_as_unread if {
	denies(rendered_layer_variables.deny, "substitutes from a ConfigMap") with input as array.concat([layer("configs", [{"kind": "ConfigMap", "name": "vars"}], uses(["INLINE"]))], vars)
}

test_an_applications_directory_with_no_layer_is_not_a_layer if {
	unlayered := {"path": "/tmp/r/unlayered.json", "contents": {"layer": {"name": "", "path": "modules/applications/a/base"}, "documents": uses(["NOBODYS"])}}
	count(rendered_layer_variables.deny) == 0 with input as array.concat(good, [unlayered])
}

test_a_layer_that_does_not_build_is_refused if {
	unbuilt := {"path": "/tmp/r/broken.json", "contents": {"layer": {"file": "f.yaml", "name": "broken", "path": "p", "unbuilt": "kustomize build: exit status 1"}, "documents": []}}
	denies(rendered_layer_variables.deny, "kustomize cannot build what the layer broken applies") with input as array.concat(good, [unbuilt])
}

test_nothing_to_read_is_refused if {
	denies(rendered_layer_variables.deny, "no layer Flux applies was rendered") with input as []
	denies(rendered_layer_variables.deny, "no layer names a variable") with input as array.concat([layer("configs", [], [])], vars)
}
