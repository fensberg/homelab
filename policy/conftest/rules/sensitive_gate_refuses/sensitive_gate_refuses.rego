# The tripwire can refuse.
#
# It is one property of the sensitive-path gate; the gate is described in
# rules.sensitive_gate_conversation.
package rules.sensitive_gate_refuses

import data.lib
import rego.v1

workflow := ".github/workflows/sensitive-paths.yml"

doc := lib.workflows[workflow]

steps contains step if {
	some job in doc.jobs
	some step in object.get(job, "steps", [])
}

run(step) := object.get(step, "run", "")

asks_a_person(step) if regex.match(`(?m)^\s*go run -C scripts/attestation \.`, run(step))

# The two conversations: the one about a sensitive path, and the one under a
# topic of its own about a key handed out.
sensitive contains step if {
	some step in steps
	asks_a_person(step)
	not contains(run(step), "-topic")
}

deny contains msg if {
	not lib.workflows[workflow]
	msg := $"{workflow} is not there, so whether the gate can refuse cannot be read"
}

deny contains msg if {
	msg := "a step is allowed to fail without failing the job, so the gate cannot refuse"
	walk(doc, [path, true])
	path[count(path) - 1] == "continue-on-error"
}

deny contains msg if {
	msg := "the acknowledgement step is not conditioned on the tripwire firing, so it either runs on every pull request or on none"
	some step in sensitive
	not contains(object.get(step, "if", ""), "steps.sensor.outputs.tripped == 'true'")
}
