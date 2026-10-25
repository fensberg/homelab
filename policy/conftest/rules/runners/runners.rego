# The estate's runners: their manifests agree with OpenTofu, each job names
# the site it runs for, and a runner keeps off the control plane and is never
# the first thing the OOM killer reaches for.
#
# The runner manifests name their namespaces as literals, while OpenTofu
# declares the same values in modules/infrastructure/platform/variables.tf and creates
# those namespaces from them.
#
# The credential secret's name is deliberately absent from this check: it is
# substituted by Flux from the same local, so the two cannot drift by
# construction. Substitution beats an assertion wherever it is available -
# this rule only exists for the values that could not be substituted, because
# a ${VAR} in metadata.name does not survive CI rendering it empty.
#
# Two declarations of one fact, which is only safe if something
# asserts they agree - the same reasoning that has registry.tf and config.go
# implementing the config contract twice.
#
# The alternative was substituting all of them through Flux, as the state
# database does. That was rejected for a boundary reason rather than a design
# one: every substituted variable has to be listed in pr-validation.yml's
# envsubst environment for kubeconform to see a complete manifest, and the
# agent that writes these files deliberately holds no `workflows` permission,
# so it cannot edit that file. Literals plus this test reach the same place
# without needing a permission the boundary is built to withhold.
package rules.runners

import data.lib
import rego.v1

releases(name) := [f | some f in lib.objects; f.contents.kind == "HelmRelease"; f.contents.metadata.name == name]

sets := [f | some f in lib.objects; f.contents.kind == "HelmRelease"; object.get(f.contents, ["spec", "chart", "spec", "chart"], "") == "gha-runner-scale-set"]

local(name) := {[f.path, v] |
	some f in lib.opentofu
	some block in object.get(f.contents, "locals", [])
	v := block[name]
	is_string(v)
}

# What one manifest file says, in every object of it.
says(path, want) if {
	some f in lib.objects
	f.path == path
	walk(f.contents, [_, v])
	is_string(v)
	contains(v, want)
}

namespaces := {"runner_system_namespace": "gha-runner-scale-set-controller", "runners_namespace": "self-hosted"}

deny contains msg if {
	some name, release in namespaces
	count(local(name)) != 1
	msg := sprintf("local.%s is declared %d time(s) beside the runner's other names, and it is one fact", [name, count(local(name))])
}

deny contains msg if {
	some name, release in namespaces
	count(releases(release)) != 1
	msg := sprintf("%d HelmRelease named %s were found, so its namespace cannot be read", [count(releases(release)), release])
}

deny contains msg if {
	some name, release in namespaces
	some [tf, want] in local(name)
	some f in releases(release)
	not says(f.path, want)
	msg := sprintf("%s declares local.%s = %q, but %s never mentions it. The manifest and the OpenTofu have drifted; one of them is now describing a resource the other does not create.", [tf, name, want, f.path])
}

# runs-on is the contract between a workflow and the scale set, and with
# runner scale sets it matches the installation name exactly rather than being
# one label among several. A rename on either side silently orphans every
# workflow targeting it - the job queues forever rather than failing.

deny contains msg if {
	not "self-hosted" in {f.contents.metadata.name | some f in sets}
	msg := sprintf("the runner scale sets found are %v, and the one every site's converge runs on (self-hosted) is not among them", [sort({f.contents.metadata.name | some f in sets})])
}

set_name(f) := object.get(f.contents, ["spec", "values", "runnerScaleSetName"], "")

deny contains msg if {
	some f in sets
	name := set_name(f)
	not per_site(name)
	msg := sprintf("the runner scale set %s is named %q. Every site reconciles this manifest, so the name must end in ${SITE} - under one fixed name, two sites offer their runners for each other's work.", [f.contents.metadata.name, name])
}

per_site(name) if {
	endswith(name, "${SITE}")
	name != "${SITE}"
}

prefixes := {trim_suffix(set_name(f), "${SITE}") | some f in sets; per_site(set_name(f))}

# The set a runs-on asks for is the longest prefix it starts with, so
# homelab-batch- is not read as homelab- with a site of batch-.
asks contains [file, label, prefix] if {
	some [file, _, job] in lib.jobs
	label := object.get(job, "runs-on", "")
	is_string(label)
	matching := [p | some p in prefixes; startswith(label, p)]
	count(matching) > 0
	longest := max({count(p) | some p in matching})
	some prefix in matching
	count(prefix) == longest
}

deny contains msg if {
	some [file, label, prefix] in asks
	site := trim_prefix(label, prefix)
	not names_a_site(site)
	msg := sprintf("%s declares `runs-on: %s`. The estate's runners are registered per site, as %s<site>, so a job names the site it runs for with an expression: %s${{ matrix.site }} for a job that runs once per site.", [file, label, prefix, prefix])
}

names_a_site(site) if {
	startswith(site, "${{")
	endswith(site, "}}")
	contains(site, "site")
}

deny contains msg if {
	count(asks) == 0
	msg := "no workflow asks for a runner of the estate's, so either nothing runs on the estate or this has stopped reading the workflows"
}

deny contains msg if {
	some p in prefixes
	not p in {prefix | some [_, _, prefix] in asks}
	msg := sprintf("no workflow asks for a runner named %s<site>, so that set of runners serves nothing. Point the job it was made for at it, or take the set out.", [p])
}

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
	some f in sets
	required(f) == null
	msg := sprintf("%s: the runner pod has no required node affinity. A preferred one is not enough: it puts CI back onto the control plane exactly when the workers are busy, which is when a build is heaviest and when etcd can least afford to share four cores with it.", [f.contents.metadata.name])
}

deny contains msg if {
	some f in sets
	some term in required(f)
	some e in object.get(term, "matchExpressions", [])
	not e.operator in modelled
	msg := sprintf("%s: this rule does not model the %q operator; teach it before using one", [f.contents.metadata.name, e.operator])
}

deny contains msg if {
	some f in sets
	admits(required(f), control_plane)
	msg := sprintf("%s: a CI runner would schedule onto a control-plane node, which is the thing the worker pool was built to stop.", [f.contents.metadata.name])
}

deny contains msg if {
	some f in sets
	terms := required(f)
	terms != null
	not admits(terms, worker)
	msg := sprintf("%s: a CI runner would not schedule onto a worker either, so this affinity takes CI off the estate rather than moving it.", [f.contents.metadata.name])
}

# A runner with no requests is BestEffort, which is the first cgroup the OOM
# controller reaches for - it is what killed the integration run in #234. The
# requests are also what the scheduler reserves, so a build lands on a node
# that can hold it rather than finding out at minute twelve.
#
# Deliberately no assertion that a limit is absent. That is a judgement this
# rule should not freeze: #234 argues against a memory limit because capping a
# build turns an infrastructure shortfall into a red test, but that reasoning
# belongs in the record and in review, not in a check that would fail the day
# somebody has a good reason.

containers(f) := object.get(f.contents, ["spec", "values", "template", "spec", "containers"], [])

deny contains msg if {
	some f in sets
	count(containers(f)) == 0
	msg := sprintf("the runner scale set %s declares no containers, so this rule is asserting nothing", [f.contents.metadata.name])
}

deny contains msg if {
	some f in sets
	some c in containers(f)
	some want in ["cpu", "memory"]
	object.get(c, ["resources", "requests", want], "") == ""
	msg := sprintf("%s: container %q requests no %s, so it is BestEffort and the OOM controller will choose it first (#234)", [f.contents.metadata.name, c.name, want])
}
