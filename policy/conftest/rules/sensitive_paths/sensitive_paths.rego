# The sensitive-path gate: a change to a sensitive path, or a key handed
# out, is asked about in a conversation somebody other than its author has to
# resolve, keyed to the content it asks about.
package rules.sensitive_paths

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

challenges(step) if regex.match(`^go run -C scripts/security \. challenge-keys -base "\$BASE" -head "\$HEAD" >>\s*"\$GITHUB_OUTPUT"$`, trim_space(run(step)))

# The two conversations: the one about a sensitive path, and the one under a
# topic of its own about a key handed out.
sensitive contains step if {
	some step in steps
	asks_a_person(step)
	not contains(run(step), "-topic")
}

ask contains step if {
	some step in steps
	asks_a_person(step)
	contains(run(step), "-topic")
}

challenge contains step if {
	some step in steps
	challenges(step)
}

# The tripwire opens a conversation.

deny contains msg if {
	not workflow in object.keys(lib.workflows)
	msg := sprintf("%s is not there, so nothing asks about a sensitive change", [workflow])
}

deny contains msg if {
	doc
	count(sensitive) == 0
	msg := "the workflow does not run scripts/attestation. Then nothing opens the review conversation a sensitive change has to be acknowledged in, and the gate is a comment nobody has to answer."
}

deny contains msg if {
	some step in sensitive
	not contains(run(step), "-digest")
	msg := "the attestation is not keyed to a digest of the sensitive diff, so an acknowledgement would outlive the change it acknowledged - which is the exact defect the label had"
}

deny contains msg if {
	"pull_request_review_thread" in lib.triggers(doc)
	msg := "the workflow triggers on pull_request_review_thread, which is not a workflow trigger. It is a webhook event only, so this workflow would never run - and anything relying on it to re-run after a resolution is a deadlock."
}

# The digest covers only the sensitive files.

digest contains step if {
	some step in steps
	step.name == "Digest the sensitive part of the diff"
}

deny contains msg if {
	doc
	count(digest) == 0
	msg := "nothing digests the sensitive part of the diff, so the acknowledgement is bound to a moment rather than to content"
}

deny contains msg if {
	some step in digest
	not reads_the_file_list(step)
	msg := "the digest does not read the sensor's file list, so it is not scoped to the sensitive files"
}

reads_the_file_list(step) if contains(run(step), "outputs.files")

reads_the_file_list(step) if {
	some v in object.get(step, "env", {})
	contains(v, "outputs.files")
}

# The tripwire can refuse.

deny contains msg if {
	walk(doc, [path, true])
	path[count(path) - 1] == "continue-on-error"
	msg := "a step is allowed to fail without failing the job, so the gate cannot refuse"
}

deny contains msg if {
	some step in sensitive
	not contains(object.get(step, "if", ""), "steps.sensor.outputs.tripped == 'true'")
	msg := "the acknowledgement step is not conditioned on the tripwire firing, so it either runs on every pull request or on none"
}

# A widened key is asked about in its own conversation.

deny contains msg if {
	doc
	count(challenge) == 0
	msg := "no step runs security challenge-keys against the pull request's base and head and writes what it finds to the step's outputs, so a key handed out is never worked out"
}

deny contains msg if {
	some step in challenge
	step["if"]
	msg := sprintf("the step that works out which keys were handed out runs only if %q; it has to run on every pull request", [step["if"]])
}

deny contains msg if {
	some step in challenge
	object.get(step, "id", "") == ""
	msg := "the step that works out which keys were handed out has no id, so nothing can read what it found"
}

deny contains msg if {
	doc
	count(sensitive) + count(ask) > 0
	not two_conversations
	msg := "the workflow does not open two conversations - one for a sensitive path and one, under a topic of its own, for a key handed out"
}

two_conversations if {
	count(sensitive) > 0
	count(ask) > 0
}

outputs := sprintf("steps.%s.outputs.", [c.id]) if some c in challenge

deny contains msg if {
	some step in ask
	not contains(object.get(step, "if", ""), sprintf("%sasked == 'true'", [outputs]))
	msg := sprintf("the step that asks runs if %q, not exactly when a key was handed out", [object.get(step, "if", "")])
}

deny contains msg if {
	some step in ask
	some name, from in {"DIGEST": "digest", "ANCHOR": "anchor", "REPORT": "report"}
	given := object.get(object.get(step, "env", {}), name, "")
	not contains(given, concat("", [outputs, from]))
	msg := sprintf("the step that asks takes its %s from %q, not from what security found, so the conversation is not keyed to the keys handed out", [name, given])
}

deny contains msg if {
	some step in ask
	not own_topic(run(step))
	msg := "the conversation about keys is opened under the same topic as the sensitive-path one, so each would withdraw the other as superseded"
}

own_topic(script) if {
	some m in regex.find_all_string_submatch_n(`(?m)^\s+-topic ([a-z]+) \\$`, script, 1)
	m[1] != "sensitive"
}

# The label mechanism is gone. Two mechanisms for one acknowledgement is two
# things to keep in step, and the weaker one becomes the one people use.

dead := ["removeLabel", "addLabels", "--json labels", "labeled"]

deny contains msg if {
	walk(doc, [path, v])
	some s in array.concat([v | is_string(v)], [k | some k in path; is_string(k)])
	some word in dead
	contains(s, word)
	msg := sprintf("the workflow still manipulates or reads labels (%q). The acknowledgement is a conversation now; a label alongside it is a second, weaker gate that will drift from this one.", [word])
}
