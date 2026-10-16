# The gate has to stand in front of the delivery in CI too.
#
# `pre-commit run` installs every hook repository before it runs any hook,
# cloning it and building its environment - which executes setup code. So a
# guard that is itself a hook reports after the download it exists to prevent,
# however early it is ordered and whatever fail_fast says. That is why
# githooks/pre-commit exists on a workstation, and the Format lane had no
# equivalent: its first act was `pre-commit run`, and seven third-party
# repositories were fetched and executed on the runner before anything could
# object.
#
# The lane now runs the same program first, with -before-install. Ordering is
# the entire property - a gate after the delivery is a receipt - and ordering
# is exactly what a test asserting "the step exists" would not notice.
package rules.format_lane

import data.lib
import rego.v1

gate := "guard-deliveries -before-install"

# The Format lane: the job that runs pre-commit over every file.
lanes contains [file, name, job] if {
	some [file, name, job] in lib.jobs
	some step in object.get(job, "steps", [])
	contains(object.get(step, "run", ""), "pre-commit run --all-files")
}

first(job, words) := min({i | some i, step in job.steps; contains(object.get(step, "run", ""), words)})

deny contains msg if {
	count(lanes) == 0
	msg := "no job runs pre-commit run --all-files, so the Format lane cannot be read"
}

deny contains msg if {
	some [file, name, job] in lanes
	not first(job, gate)
	msg := sprintf("%s: the Format lane (%s) does not run the supplier gate before installing. Without it, `pre-commit run` clones and installs every third-party hook repository on the runner before anything can refuse one - so an unapproved supplier added in a pull request executes its setup code before any check says no.", [file, name])
}

deny contains msg if {
	some [file, name, job] in lanes
	at := first(job, gate)
	some later in ["scripts/take-delivery.sh", "pre-commit run --all-files"]
	first(job, later) < at
	msg := sprintf("%s: the Format lane runs %q before the supplier gate. The gate then reports on code that has already been downloaded and had its setup executed, which is a receipt rather than a refusal.", [file, later])
}

# And it sets up Go on the toolchain the estate pins: by reading GO_VERSION
# itself, or through the shared setup action, which takes no version of its
# own and reads the pin.

reads_the_pin(job) if {
	walk(job, [_, v])
	is_string(v)
	contains(v, "${{ env.GO_VERSION }}")
}

reads_the_pin(job) if {
	some step in job.steps
	lib.same_repo_action(object.get(step, "uses", "")) == ".github/workflows/setup-go"
	shared := lib.actions[".github/workflows/setup-go"]
	not "go-version" in object.keys(object.get(shared, "inputs", {}))
	walk(shared, [_, v])
	v == "${{ env.GO_VERSION }}"
}

deny contains msg if {
	some [file, name, job] in lanes
	not reads_the_pin(job)
	msg := sprintf("%s: the Format lane sets up Go without reading GO_VERSION from scripts/versions.env, so it can validate on a different toolchain than the one the estate pins", [file])
}
