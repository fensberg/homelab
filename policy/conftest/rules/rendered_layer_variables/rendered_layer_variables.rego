# Every layer Flux applies is given each variable it names.
#
# Flux fills a ${VAR} from the secrets a layer says it substitutes from, and
# each layer names its own: the controllers are given the monitoring and
# storage secrets, the layer after them the site's addresses and names. A
# layer that names a variable none of its secrets carries is refused whole.
# Nothing in it is applied, everything behind it waits, and the manifests
# look right: validation fills every variable for every layer alike, so a
# variable in the wrong layer validates.
#
# That is what happened on 2026-10-12. A setting in the controllers layer
# named a variable that is in the next layer's secret, and the controllers
# layer stopped applying, with two new tools in it.
#
# So this reads both ends. What a layer names is every variable in what
# kustomize builds from its path, rendered by the harness. What it is given
# is its own substitutions and the keys of the secrets it lists, read off
# each site's plan as OpenTofu would write them, every key worked out.
#
# AND BY BOTH THE CHECKOUT AND THE RELEASE A SITE RUNS. Flux applies what
# merges at once, while a site's secrets are written by the release its line
# pins, until the line moves. A key only the checkout's modules write refused
# infra-configs from the merge until the release that wrote it reached the
# site (#843). So a variable is given only where both write it: the release
# lands and the site's line moves first, and the manifest follows.
package rules.rendered_layer_variables

import data.lib
import rego.v1

layers contains [layer, docs] if {
	some [_, layer, docs] in lib.rendered_layers
	layer.name != ""
}

named(docs) := {m[1] |
	walk(docs, [_, v])
	is_string(v)
	some m in regex.find_all_string_submatch_n(`\$\{([A-Z][A-Z0-9_]*)\}`, v, -1)
}

# Every secret a site's plan writes, as [plan, namespace, name, keys]: keys
# null where the plan cannot say them. The checkout's plans, and those made
# with the release each site runs.
secrets contains [path, s.metadata[0].namespace, s.metadata[0].name, keys_of(s)] if {
	some [path, r] in lib.plan
	lib.plan_scope(path) == "site"
	r.type == "kubernetes_secret"
	s := r.values
}

pinned_secrets contains [path, s.metadata[0].namespace, s.metadata[0].name, keys_of(s)] if {
	some [path, r] in lib.pinned_plan
	r.type == "kubernetes_secret"
	s := r.values
}

keys_of(s) := object.keys(s.data) if is_object(s.data)

keys_of(s) := null if not is_object(s.data)

site_plans contains path if some [path, _, _, _] in secrets

pinned_plans contains path if some [path, _, _, _] in pinned_secrets

# A variable a layer is given: by its own substitutions, or by one of the
# secrets it substitutes from, carrying it in every site's plan - the
# checkout's, and the release's each site runs.
given(layer, v) if inline(layer, v)

given(layer, v) if {
	carried(layer, v, secrets, site_plans)
	carried(layer, v, pinned_secrets, pinned_plans)
}

inline(layer, v) if object.get(object.get(layer, "substitute", {}), v, null) != null

carried(layer, v, written, plans) if {
	some from in object.get(layer, "substitute_from", [])
	from.kind == "Secret"
	carriers := {p | some [p, ns, name, keys] in written; ns == layer.in_namespace; name == from.name; is_set(keys); v in keys}
	count(carriers) == count(plans)
}

deny contains msg if {
	some [layer, docs] in layers
	some v in named(docs)
	not given(layer, v)
	not carried(layer, v, secrets, site_plans)
	not carried(layer, v, pinned_secrets, pinned_plans)
	msg := $"{layer.file}: the layer {layer.name} names $\{{v}} in what it applies ({layer.path}), and neither its own substitutions nor any secret it substitutes from carries it. Flux refuses the whole layer, and everything behind it waits. Substitute it from a secret that carries it, or move the setting to the layer whose secret does."
}

deny contains msg if {
	some [layer, docs] in layers
	some v in named(docs)
	not inline(layer, v)
	carried(layer, v, secrets, site_plans)
	not carried(layer, v, pinned_secrets, pinned_plans)
	msg := $"{layer.file}: the layer {layer.name} names $\{{v}} in what it applies ({layer.path}), and only the checkout's modules write it, not the release a site runs. Flux applies this on the merge, before the site runs a release that writes it, and refuses the whole layer. Land the module that writes it, release it and move the site's line first; then use it."
}

deny contains msg if {
	some [layer, docs] in layers
	some v in named(docs)
	not inline(layer, v)
	carried(layer, v, pinned_secrets, pinned_plans)
	not carried(layer, v, secrets, site_plans)
	msg := $"{layer.file}: the layer {layer.name} names $\{{v}} in what it applies ({layer.path}), which the release a site runs writes and the checkout's modules no longer do. Once the site's line moves, Flux refuses the whole layer. Take the manifest's use of it out first, or keep writing it."
}

deny contains msg if {
	count(pinned_plans) == 0
	msg := "no site's plan was made with the release it runs, so what Flux applies was held to the checkout's modules only"
}

deny contains msg if {
	some [layer, _] in layers
	some from in object.get(layer, "substitute_from", [])
	from.kind != "Secret"
	msg := $"{layer.file}: the layer {layer.name} substitutes from a {from.kind}, and only a Secret's keys are read here, so what it is given was not checked"
}

deny contains msg if {
	some [layer, _] in layers
	some from in object.get(layer, "substitute_from", [])
	from.kind == "Secret"
	count({p | some [p, ns, name, _] in secrets; ns == layer.in_namespace; name == from.name}) == 0
	msg := $"{layer.file}: the layer {layer.name} substitutes from the secret {layer.in_namespace}/{from.name}, which no site's plan writes. Flux refuses a layer whose secret is not there."
}

deny contains msg if {
	some [layer, _] in layers
	some from in object.get(layer, "substitute_from", [])
	from.kind == "Secret"
	count({p | some [p, ns, name, _] in secrets; ns == layer.in_namespace; name == from.name}) > 0
	count({p | some [p, ns, name, _] in pinned_secrets; ns == layer.in_namespace; name == from.name}) == 0
	msg := $"{layer.file}: the layer {layer.name} substitutes from the secret {layer.in_namespace}/{from.name}, which only the checkout's modules write, not the release a site runs. Flux refuses the layer from the merge until the site runs a release that writes it: release it and move the site's line first."
}

deny contains msg if {
	some [layer, _] in layers
	some from in object.get(layer, "substitute_from", [])
	from.kind == "Secret"
	some [_, ns, name, keys] in (secrets | pinned_secrets)
	ns == layer.in_namespace
	name == from.name
	keys == null
	msg := $"{layer.file}: the layer {layer.name} substitutes from the secret {ns}/{name}, whose keys a plan cannot show, so what it is given cannot be read"
}

deny contains msg if {
	some [layer, _] in layers
	layer.unbuilt
	msg := $"{layer.file}: kustomize cannot build what the layer {layer.name} applies ({layer.path}), so what it names was not read: {layer.unbuilt}"
}

deny contains msg if {
	count(layers) == 0
	msg := "no layer Flux applies was rendered, so nothing was held to what it is given"
}

deny contains msg if {
	count({v | some [_, docs] in layers; some v in named(docs)}) == 0
	msg := "no layer names a variable, so nothing was held to anything. The manifests here do name them, so what Flux applies was not read."
}
