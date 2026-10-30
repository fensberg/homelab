# The digest covers only the sensitive files.
#
# It is one property of the sensitive-path gate; the gate is described in
# rules.sensitive_gate_conversation.
package rules.sensitive_gate_digest

import data.lib
import rego.v1

workflow := ".github/workflows/sensitive-paths.yml"

doc := lib.workflows[workflow]

steps contains step if {
	some job in doc.jobs
	some step in object.get(job, "steps", [])
}

run(step) := object.get(step, "run", "")

digest contains step if {
	some step in steps
	step.name == "Digest the sensitive part of the diff"
}

deny contains msg if {
	not lib.workflows[workflow]
	msg := $"{workflow} is not there, so what the acknowledgement is bound to cannot be read"
}

deny contains msg if {
	doc
	count(digest) == 0
	msg := "nothing digests the sensitive part of the diff, so the acknowledgement is bound to a moment rather than to content"
}

deny contains msg if {
	msg := "the digest does not read the sensor's file list, so it is not scoped to the sensitive files"
	some step in digest
	not reads_the_file_list(step)
}

reads_the_file_list(step) if contains(run(step), "outputs.files")

reads_the_file_list(step) if {
	some v in object.get(step, "env", {})
	contains(v, "outputs.files")
}
