# The Format lane sets up Go on the toolchain the estate pins: by reading
# GO_VERSION itself, or through the shared setup action, which takes no version
# of its own and reads the pin. A lane on a toolchain of its own validates
# against a Go the estate does not run. That it runs the supplier gate before
# it installs anything is rules.format_lane.
package rules.format_lane_go

import data.lib
import rego.v1

# The Format lane: the job that runs pre-commit over every file.
lanes contains [file, name, job] if {
	some [file, name, job] in lib.jobs
	some step in object.get(job, "steps", [])
	contains(object.get(step, "run", ""), "pre-commit run --all-files")
}

reads_the_pin(job) if {
	walk(job, [_, v])
	is_string(v)
	contains(v, "${{ env.GO_VERSION }}")
}

reads_the_pin(job) if {
	shared := lib.actions[".github/workflows/setup-go"]
	not "go-version" in object.keys(object.get(shared, "inputs", {}))
	some step in job.steps
	lib.same_repo_action(object.get(step, "uses", "")) == ".github/workflows/setup-go"
	walk(shared, [_, v])
	v == "${{ env.GO_VERSION }}"
}

deny contains msg if {
	count(lanes) == 0
	msg := "no job runs pre-commit run --all-files, so the Format lane's Go cannot be read"
}

deny contains msg if {
	some [file, name, job] in lanes
	not reads_the_pin(job)
	msg := $"{file}: the Format lane sets up Go without reading GO_VERSION from scripts/versions.env, so it can validate on a different toolchain than the one the estate pins"
}
