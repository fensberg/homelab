# What Flux applies needs only what every site's pinned release makes.
#
# Flux applies what merges at once. What it applies leans on things OpenTofu
# makes - the namespaces it is put in, the secrets and config maps a release
# reads its values from, the accounts its pods run as - and a site's OpenTofu
# is the release its line pins, until the line moves. A manifest that needs
# something only the checkout's modules make is applied on the merge, against
# a site that does not have it yet: infra-configs was refused from the merge
# of #835 until the release that wrote what it needed reached the site
# (#843). So each is held three ways: made by what Flux applies itself, or by
# both the checkout and the release each site runs. Made by the checkout
# alone, the release lands and the site's line moves first; made by the
# release alone, the manifest's use of it goes first.
#
# A name written as one of a layer's variables is the value the layer's own
# secret gives it, in each plan: the namespace the database is put in is
# ${STATE_DB_NAMESPACE}. One no secret gives is rendered_layer_variables'.
package rules.rendered_platform_dependencies

import data.lib
import rego.v1

layers contains [layer, docs] if {
	some [_, layer, docs] in lib.rendered_layers
	layer.name != ""
}

# What each layer needs of the platform, as [layer, kind, namespace, name]:
# a namespace by its name alone.
needs contains [layer, "Namespace", "", doc.metadata.namespace] if {
	some [layer, docs] in layers
	some doc in docs
	is_string(doc.metadata.namespace)
}

needs contains [layer, "Namespace", "", doc.spec.targetNamespace] if {
	some [layer, docs] in layers
	some doc in docs
	doc.kind == "HelmRelease"
	is_string(doc.spec.targetNamespace)
	not doc.spec.install.createNamespace
}

needs contains [layer, from.kind, doc.metadata.namespace, from.name] if {
	some [layer, docs] in layers
	some doc in docs
	doc.kind == "HelmRelease"
	some from in object.get(doc.spec, "valuesFrom", [])
}

needs contains [layer, "ServiceAccount", doc.metadata.namespace, account] if {
	some [layer, docs] in layers
	some doc in docs
	some pod in pod_specs(doc)
	account := pod.serviceAccountName
}

pod_specs(doc) := {doc.spec.template.spec} if doc.kind in {"Deployment", "StatefulSet", "DaemonSet", "Job"}

pod_specs(doc) := {doc.spec.jobTemplate.spec.template.spec} if doc.kind == "CronJob"

# What Flux applies makes itself, in any layer.
by_manifest contains [doc.kind, object.get(doc.metadata, "namespace", ""), doc.metadata.name] if {
	some [_, docs] in layers
	some doc in docs
	doc.kind in {"Namespace", "ServiceAccount", "Secret", "ConfigMap"}
}

# What OpenTofu makes in a plan, as [plan, kind, namespace, name].
tofu_kinds := {
	"kubernetes_namespace": "Namespace",
	"kubernetes_service_account": "ServiceAccount",
	"kubernetes_secret": "Secret",
	"kubernetes_config_map": "ConfigMap",
}

by_checkout contains [path, tofu_kinds[r.type], object.get(r.values.metadata[0], "namespace", ""), r.values.metadata[0].name] if {
	some [path, r] in lib.plan
	lib.plan_scope(path) == "site"
	tofu_kinds[r.type]
}

by_release contains [path, tofu_kinds[r.type], object.get(r.values.metadata[0], "namespace", ""), r.values.metadata[0].name] if {
	some [path, r] in lib.pinned_plan
	tofu_kinds[r.type]
}

site_plans contains path if some [path, _, _, _] in by_checkout

release_plans contains path if some [path, _, _, _] in by_release

# A name as a plan's own secrets resolve it for a layer: written out, or the
# value of a variable the layer substitutes from that plan's secrets.
resolved(_, name, _) := name if not contains(name, "${")

resolved(layer, name, path) := value if {
	some m in regex.find_all_string_submatch_n(`^\$\{([A-Z][A-Z0-9_]*)\}$`, name, 1)
	some from in object.get(layer, "substitute_from", [])
	some [p, r] in (lib.plan | lib.pinned_plan)
	p == path
	r.type == "kubernetes_secret"
	r.values.metadata[0].name == from.name
	value := r.values.data[m[1]]
}

# Made in every plan of a set: each plan by its own reading of the names.
made_in(layer, kind, ns, name, made, plans) if {
	count(plans) > 0
	every path in plans {
		[resolved_or_empty(layer, ns, path), resolved(layer, name, path)] in made_by(made, path, kind)
	}
}

# What one plan makes of one kind, as [namespace, name].
made_by(made, path, kind) := {[made_ns, made_name] | some [p, k, made_ns, made_name] in made; p == path; k == kind}

resolved_or_empty(_, "", _) := ""

resolved_or_empty(layer, ns, path) := resolved(layer, ns, path) if ns != ""

made_by_manifest(kind, ns, name) if [kind, ns, name] in by_manifest

# A name that is a variable no secret gives is not read here.
resolvable(_, name) if not contains(name, "${")

resolvable(layer, name) if {
	contains(name, "${")
	some path in site_plans
	resolved(layer, name, path)
}

where("", name) := name

where(ns, name) := $"{ns}/{name}" if ns != ""

deny contains msg if {
	some [layer, kind, ns, name] in needs
	not made_by_manifest(kind, ns, name)
	resolvable(layer, name)
	not made_in(layer, kind, ns, name, by_checkout, site_plans)
	not made_in(layer, kind, ns, name, by_release, release_plans)
	msg := $"{layer.file}: what the layer {layer.name} applies ({layer.path}) needs the {kind} {where(ns, name)}, and nothing makes it: not what Flux applies, not the checkout's modules, not the release a site runs."
}

deny contains msg if {
	some [layer, kind, ns, name] in needs
	not made_by_manifest(kind, ns, name)
	made_in(layer, kind, ns, name, by_checkout, site_plans)
	not made_in(layer, kind, ns, name, by_release, release_plans)
	msg := $"{layer.file}: what the layer {layer.name} applies ({layer.path}) needs the {kind} {where(ns, name)}, which only the checkout's modules make, not the release a site runs. Flux applies this on the merge, before the site has it. Land the module that makes it, release it and move the site's line first; then use it."
}

deny contains msg if {
	some [layer, kind, ns, name] in needs
	not made_by_manifest(kind, ns, name)
	made_in(layer, kind, ns, name, by_release, release_plans)
	not made_in(layer, kind, ns, name, by_checkout, site_plans)
	msg := $"{layer.file}: what the layer {layer.name} applies ({layer.path}) needs the {kind} {where(ns, name)}, which the release a site runs makes and the checkout's modules no longer do. Once the site's line moves it is gone: take the manifest's need of it out first, or keep making it."
}

deny contains msg if {
	count(needs) == 0
	msg := "nothing Flux applies was read as needing anything of the platform, so nothing was held to what makes it"
}

deny contains msg if {
	count(release_plans) == 0
	msg := "no site's plan was made with the release it runs, so what Flux applies was not held to it"
}
