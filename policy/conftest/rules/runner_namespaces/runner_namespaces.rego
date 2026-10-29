# Each set of runners, and their controller, is in the namespace the estate
# makes for it. The estate's other runner rules are its names
# (rules.runner_names), where its pods may land (rules.runner_affinity) and
# what they ask for (rules.runner_requests).
#
# The namespaces are the fact config/facts/runner-namespaces.json, which the
# platform module creates them from. A HelmRelease writes its namespace out
# rather than taking it from Flux: the engineer tries a change by loading the
# runners' pods into a cluster of its own before anything substitutes them,
# and a ${VAR} there names no namespace at all, as a ${VAR} in a toleration
# named no taint. So the written namespace is held to the fact here, field to
# field: a release in a namespace the estate does not make is never
# installed, and one in another runner's namespace is held by that runner's
# walls rather than its own.
#
# Each release is known by what it is - the chart it installs, and for a set
# of runners whether it is a pull-request pool - and not by its name.
package rules.runner_namespaces

import data.lib
import rego.v1

namespaces := lib.facts["runner-namespaces"]

releases(chart) := [f | some f in lib.objects; f.contents.kind == "HelmRelease"; f.contents.spec.chart.spec.chart == chart]

# Where each release belongs: the controller with the controller, a pool with
# the pull-request runners, and every other set with the estate's runners.
belongs contains [f, namespaces.system, "the runners' controller"] if {
	some f in releases("gha-runner-scale-set-controller")
}

belongs contains [f, namespaces.pull_request_runners, "a pull-request pool"] if {
	some f in pools
}

belongs contains [f, namespaces.runners, "a set of the estate's own runners"] if {
	some f in own_sets
}

pools contains f if {
	some f in releases("gha-runner-scale-set")
	lib.pull_request_pool(f.contents.spec.values.runnerScaleSetName)
}

own_sets contains f if {
	some f in releases("gha-runner-scale-set")
	not lib.pull_request_pool(f.contents.spec.values.runnerScaleSetName)
}

deny contains msg if {
	some [f, want, what] in belongs
	got := object.get(f.contents.metadata, "namespace", "")
	got != want
	msg := $"{f.path}: {f.contents.metadata.name} is {what}, and is in the namespace \"{got}\" where config/facts/runner-namespaces.json makes \"{want}\" for it. In a namespace the estate does not make it is never installed; in another runner's it is held by that runner's walls"
}

deny contains msg if {
	some key in ["system", "runners", "pull_request_runners"]
	not is_string(object.get(object.union_n([{}, object.get(lib.facts, "runner-namespaces", {})]), key, null))
	msg := $"config/facts/runner-namespaces.json gives no {key} namespace, so where those runners belong cannot be read"
}

deny contains msg if {
	count(releases("gha-runner-scale-set-controller")) == 0
	msg := "no HelmRelease installs the runners' controller, so its namespace was not read"
}

deny contains msg if {
	count(own_sets) == 0
	msg := "no set of the estate's own runners was read, so their namespace was not held"
}

deny contains msg if {
	count(pools) == 0
	msg := "no pull-request pool was read, so their namespace was not held"
}
