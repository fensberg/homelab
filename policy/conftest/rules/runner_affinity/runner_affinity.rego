# CI must never be schedulable onto a control plane, and must be schedulable
# onto a worker.
#
# The first half is the safety property: eight concurrent jobs pulling large
# images alongside etcd is how a control plane becomes intermittently unwell,
# and one integration run was already killed for competing with it (#236).
# The second half is the guard against overcorrecting - an affinity so narrow
# that nothing schedules would satisfy the first assertion perfectly while
# taking CI off the estate entirely.
#
# The affinity is evaluated rather than matched.
#
# A rule asserting that the manifest contains the string "DoesNotExist" passes
# just as happily when the key is wrong, when the operator has been flipped to
# Exists, or when the whole term has been moved into the `preferred` block -
# where it excludes nothing at all. So this implements the small part of the
# scheduler's own rule that the property depends on, and asks it the two
# questions that matter: would this pod land on a control plane, and can it
# land anywhere.
#
# Terms are OR'd; the expressions inside one term are AND'd. That is the
# scheduler's semantics, and getting it backwards is exactly the mistake this
# is here to catch.
package rules.runner_affinity

import data.lib
import rego.v1

sets := [f | some f in lib.objects; f.contents.kind == "HelmRelease"; f.contents.spec.chart.spec.chart == "gha-runner-scale-set"]

control_plane := {"kubernetes.io/os": "linux", "node-role.kubernetes.io/control-plane": ""}

worker := {"kubernetes.io/os": "linux"}

modelled := {"Exists", "DoesNotExist"}

admits(terms, labels) if {
	some term in terms
	every e in object.get(term, "matchExpressions", []) {
		holds(e, labels)
	}
	count(object.get(term, "matchExpressions", [])) > 0
}

holds(e, labels) if {
	e.operator == "Exists"
	e.key in object.keys(labels)
}

holds(e, labels) if {
	e.operator == "DoesNotExist"
	not e.key in object.keys(labels)
}

required(f) := object.get(f.contents, ["spec", "values", "template", "spec", "affinity", "nodeAffinity", "requiredDuringSchedulingIgnoredDuringExecution", "nodeSelectorTerms"], null)

deny contains msg if {
	count(sets) == 0
	msg := "no runner scale set was found, so where its pods may land cannot be read"
}

deny contains msg if {
	some f in sets
	required(f) == null
	msg := $"{f.contents.metadata.name}: the runner pod has no required node affinity. A preferred one is not enough: it puts CI back onto the control plane exactly when the workers are busy, which is when a build is heaviest and when etcd can least afford to share four cores with it."
}

deny contains msg if {
	some f in sets
	some term in required(f)
	some e in object.get(term, "matchExpressions", [])
	not e.operator in modelled
	msg := $"{f.contents.metadata.name}: this rule does not model the \"{e.operator}\" operator; teach it before using one"
}

deny contains msg if {
	some f in sets
	admits(required(f), control_plane)
	msg := $"{f.contents.metadata.name}: a CI runner would schedule onto a control-plane node, which is the thing the worker pool was built to stop."
}

deny contains msg if {
	some f in sets
	terms := required(f)
	terms != null
	not admits(terms, worker)
	msg := $"{f.contents.metadata.name}: a CI runner would not schedule onto a worker either, so this affinity takes CI off the estate rather than moving it."
}
