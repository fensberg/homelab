# Each runner scale set is named for the site that reconciles it, and each
# job on the estate's runners names the site it runs for.
#
# runs-on is the contract between a workflow and the scale set, and with
# runner scale sets it matches the installation name exactly rather than being
# one label among several. A rename on either side silently orphans every
# workflow targeting it - the job queues forever rather than failing.
package rules.runner_names

import data.lib
import rego.v1

# A brace of a template a message quotes, which a template string cannot
# hold as it is.
open := "{"

close := "}"

sets := [f | some f in lib.objects; f.contents.kind == "HelmRelease"; f.contents.spec.chart.spec.chart == "gha-runner-scale-set"]

set_name(f) := object.get(f.contents, ["spec", "values", "runnerScaleSetName"], "")

per_site(name) if {
	endswith(name, "${SITE}")
	name != "${SITE}"
}

prefixes contains trim_suffix(set_name(f), "${SITE}") if {
	some f in sets
	per_site(set_name(f))
}

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

names_a_site(site) if {
	startswith(site, "${{")
	endswith(site, "}}")
	contains(site, "site")
}

deny contains msg if {
	not "self-hosted" in {f.contents.metadata.name | some f in sets}
	msg := $"the runner scale sets found are {sort({f.contents.metadata.name | some f in sets})}, and the one every site's converge runs on (self-hosted) is not among them"
}

deny contains msg if {
	some f in sets
	name := set_name(f)
	not per_site(name)
	msg := sprintf("the runner scale set %s is named %q. Every site reconciles this manifest, so the name must end in ${SITE} - under one fixed name, two sites offer their runners for each other's work.", [f.contents.metadata.name, name])
}

deny contains msg if {
	some [file, label, prefix] in asks
	site := trim_prefix(label, prefix)
	not names_a_site(site)
	msg := $"{file} declares `runs-on: {label}`. The estate's runners are registered per site, as {prefix}<site>, so a job names the site it runs for with an expression: {prefix}${open}{open} matrix.site {close}{close} for a job that runs once per site."
}

deny contains msg if {
	count(asks) == 0
	msg := "no workflow asks for a runner of the estate's, so either nothing runs on the estate or this has stopped reading the workflows"
}

deny contains msg if {
	some p in prefixes
	not p in {prefix | some [_, _, prefix] in asks}
	msg := $"no workflow asks for a runner named {p}<site>, so that set of runners serves nothing. Point the job it was made for at it, or take the set out."
}
