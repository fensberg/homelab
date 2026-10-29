# Every variable Flux substitutes has a stand-in, and every stand-in is used.
#
# What Flux applies carries ${VAR} placeholders that Flux fills from secrets
# OpenTofu writes. Validation substitutes stand-ins first, because a
# manifest holding the string "${STATE_DB_NODEPORT}" where a port belongs
# cannot be parsed, let alone checked.
#
# Both directions matter, and they fail differently. A variable with no
# stand-in substitutes to the EMPTY STRING: the manifest still parses -
# `nodePort:` with nothing after it is valid YAML - so validation passes on
# a document that is not the one Flux will apply, and the real failure
# waits until a reconcile. A stand-in nobody uses is a value kept current
# for a variable that was renamed or removed, which reads as coverage that
# is not there.
#
# The stand-ins are tests/flux-substitutions.env, and those validation
# derives for what the platform hands a site's applications, by the script
# it runs, which the rule harness runs too and hands over with each layer. A derived one is not held to
# being used: an estate where no site runs an application substitutes them
# into nothing. What is read is what kustomize builds of each layer, and of
# each application directory whether or not a site runs it, so a comment -
# which kustomize drops - names no variable, and what OpenTofu applies
# itself, which Flux never substitutes into, is in no layer.
#
# This does NOT check that a layer is given the variable it names: the
# stand-ins are one list for every layer. That is rendered_layer_variables.
package rules.rendered_stand_ins

import data.lib
import rego.v1

declared := object.union_n([f.contents | some f in lib.at("tests/flux-substitutions.env")])

derived := object.union_n([object.get(layer, "derived_stand_ins", {}) | some [_, layer, _] in lib.rendered_layers])

used contains [v, layer.path] if {
	some [_, layer, docs] in lib.rendered_layers
	walk(docs, [_, s])
	is_string(s)
	some m in regex.find_all_string_submatch_n(`\$\{([A-Z][A-Z0-9_]*)\}`, s, -1)
	v := m[1]
}

deny contains msg if {
	some [v, where] in used
	not declared[v]
	not derived[v]
	msg := $"{where} uses $\{{v}}, and tests/flux-substitutions.env has no stand-in for it. It substitutes to the empty string, which usually still parses - so validation passes on a manifest that is not the one Flux applies, and the failure waits for a reconcile. Add a stand-in whose SHAPE matches the real value."
}

deny contains msg if {
	some v, _ in declared
	count({w | some [v2, w] in used; v2 == v}) == 0
	msg := $"tests/flux-substitutions.env declares {v}, and nothing Flux applies or an application uses it. Either the variable was renamed or removed and this was left behind, or the manifest that needed it never landed. A stand-in for nothing reads as coverage that does not exist."
}

deny contains msg if {
	some v, _ in declared
	derived[v]
	msg := $"tests/flux-substitutions.env lists {v}, which is derived for applications. A line for it there is a stand-in that has to be taken out with the last application."
}

deny contains msg if {
	count(declared) == 0
	msg := "tests/flux-substitutions.env declares no stand-ins, so this checked nothing"
}

deny contains msg if {
	count(derived) == 0
	msg := "no stand-in was derived for the applications, so what validation substitutes into them was not read"
}
