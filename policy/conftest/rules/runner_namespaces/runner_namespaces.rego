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
#
# AND WHAT IS ABOUT THE RUNNERS IS WHERE THEY ARE. A policy or a budget
# selects a set's runners by the set's name, and selects only in its own
# namespace: one left in another selects nothing and says nothing, and the
# runners it was for are unwalled or unprotected. The sweeper is handed the
# pull-request runners' namespace to watch, and its rights to read their pods
# are granted in a namespace: told one and granted another, it sees no job
# and puts back a machine in the middle of one.
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

# The runners each set of runners makes are labelled with its name, so what
# selects them names it; the set is in the namespace its release is in.
set_label := "actions.github.com/scale-set-name"

sets contains [f.contents.spec.values.runnerScaleSetName, object.get(f.contents.metadata, "namespace", "")] if {
	some f in releases("gha-runner-scale-set")
}

selects contains [f, set] if {
	some f in lib.objects
	some key in ["endpointSelector", "podSelector", "selector"]
	set := f.contents.spec[key].matchLabels[set_label]
}

# What is handed the namespace a set of runners is in, as the sweeper is:
# the container, the namespace it was told, and the account it runs as.
told contains [f, namespace, account] if {
	some f in lib.objects
	f.contents.kind == "Deployment"
	some c in f.contents.spec.template.spec.containers
	some i, arg in object.get(c, "args", [])
	arg == "-runners"
	namespace := c.args[i + 1]
	account := object.get(f.contents.spec.template.spec, "serviceAccountName", "default")
}

granted contains [namespace, account] if {
	some f in lib.objects
	f.contents.kind == "RoleBinding"
	namespace := object.get(f.contents.metadata, "namespace", "")
	some subject in object.get(f.contents, "subjects", [])
	subject.kind == "ServiceAccount"
	account := subject.name
}

deny contains msg if {
	some [f, want, what] in belongs
	got := object.get(f.contents.metadata, "namespace", "")
	got != want
	msg := $"{f.path}: {f.contents.metadata.name} is {what}, and is in the namespace \"{got}\" where config/facts/runner-namespaces.json makes \"{want}\" for it. In a namespace the estate does not make it is never installed; in another runner's it is held by that runner's walls"
}

deny contains msg if {
	some [f, set] in selects
	some [named, want] in sets
	named == set
	got := object.get(f.contents.metadata, "namespace", "")
	got != want
	msg := $"{f.path}: {f.contents.kind} {f.contents.metadata.name} selects the runners of {set}, which are in \"{want}\", from \"{got}\". It selects only in its own namespace, so it holds nothing, and the runners it was for are left without it"
}

deny contains msg if {
	some [f, set] in selects
	count({ns | some [named, ns] in sets; named == set}) == 0
	msg := $"{f.path}: {f.contents.kind} {f.contents.metadata.name} selects the runners of {set}, and no release makes a set of that name, so it selects nothing"
}

deny contains msg if {
	some [f, namespace, _] in told
	namespace != namespaces.pull_request_runners
	msg := $"{f.path}: {f.contents.metadata.name} is told the pull-request runners are in \"{namespace}\", where config/facts/runner-namespaces.json makes \"{namespaces.pull_request_runners}\". It would watch a namespace with no runner in it, see no job, and put back machines mid-job"
}

deny contains msg if {
	some [f, namespace, account] in told
	not [namespace, account] in granted
	msg := $"{f.path}: {f.contents.metadata.name} watches the runners in \"{namespace}\" as {account}, and nothing grants {account} anything there, so it can read none of their pods"
}

deny contains msg if {
	count(selects) == 0
	msg := "nothing selects a set of runners, so where the runners' walls are was not read"
}

deny contains msg if {
	count(told) == 0
	msg := "nothing is told where the pull-request runners are, so the sweeper's reading of them was not held"
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
