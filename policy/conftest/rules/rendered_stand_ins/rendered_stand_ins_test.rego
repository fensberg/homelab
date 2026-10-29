package rules.rendered_stand_ins_test

import data.rules.rendered_stand_ins
import rego.v1

rendered(path, names) := {"path": $"/tmp/r/{path}.json", "contents": {
	"layer": {"name": "l", "path": path, "derived_stand_ins": {"NAME_APP": "app.site.example"}},
	"documents": [{"kind": "ConfigMap", "data": {n: $"$\{{n}}" | some n in names}}],
}}

env(names) := {"path": "tests/flux-substitutions.env", "contents": {n: "x" | some n in names}}

good := [rendered("clusters/core", ["SITE", "NAME_APP"]), env(["SITE"])]

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_every_variable_with_its_stand_in_passes if {
	count(rendered_stand_ins.deny) == 0 with input as good
}

test_a_variable_with_no_stand_in_is_refused if {
	denies(rendered_stand_ins.deny, "clusters/core uses ${MISSING}") with input as [rendered("clusters/core", ["SITE", "MISSING"]), env(["SITE"])]
}

test_a_stand_in_nothing_uses_is_refused if {
	denies(rendered_stand_ins.deny, "declares GONE, and nothing Flux applies") with input as [rendered("clusters/core", ["SITE"]), env(["SITE", "GONE"])]
}

test_a_derived_stand_in_listed_too_is_refused if {
	denies(rendered_stand_ins.deny, "lists NAME_APP, which is derived for applications") with input as [rendered("clusters/core", ["SITE", "NAME_APP"]), env(["SITE", "NAME_APP"])]
}

test_nothing_to_read_is_refused if {
	denies(rendered_stand_ins.deny, "declares no stand-ins") with input as []
	denies(rendered_stand_ins.deny, "no stand-in was derived") with input as [{"path": "/tmp/r/x.json", "contents": {"layer": {"name": "l", "path": "p"}, "documents": []}}]
}
