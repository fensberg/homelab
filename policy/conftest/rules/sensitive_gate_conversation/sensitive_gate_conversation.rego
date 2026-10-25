# The sensitive-path gate: a change to a sensitive path, or a key handed
# out, is asked about in a conversation somebody other than its author has to
# resolve, keyed to the content it asks about. Held here: the tripwire opens
# the conversation, keyed to a digest. The gate's other properties are its
# digest (rules.sensitive_gate_digest), that it can refuse
# (rules.sensitive_gate_refuses), the conversation about keys
# (rules.sensitive_gate_keys) and that no label stands in for either
# (rules.sensitive_gate_labels).
package rules.sensitive_gate_conversation

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

# The tripwire opens a conversation.

deny contains msg if {
	not workflow in object.keys(lib.workflows)
	msg := $"{workflow} is not there, so nothing asks about a sensitive change"
}

deny contains msg if {
	doc
	count(sensitive) == 0
	msg := "the workflow does not run scripts/attestation. Then nothing opens the review conversation a sensitive change has to be acknowledged in, and the gate is a comment nobody has to answer."
}

deny contains msg if {
	msg := "the attestation is not keyed to a digest of the sensitive diff, so an acknowledgement would outlive the change it acknowledged - which is the exact defect the label had"
	some step in sensitive
	not contains(run(step), "-digest")
}

deny contains msg if {
	"pull_request_review_thread" in lib.triggers(doc)
	msg := "the workflow triggers on pull_request_review_thread, which is not a workflow trigger. It is a webhook event only, so this workflow would never run - and anything relying on it to re-run after a resolution is a deadlock."
}
