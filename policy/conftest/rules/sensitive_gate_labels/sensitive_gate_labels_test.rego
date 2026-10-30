package rules.sensitive_gate_labels_test

import data.rules.sensitive_gate_labels
import rego.v1

sensor := {"id": "sensor", "run": "git diff --name-only \"$BASE...$HEAD\" | .github/scripts/sensitive-paths.sh >>\"$GITHUB_OUTPUT\""}

digest := {"name": "Digest the sensitive part of the diff", "id": "digest", "if": "steps.sensor.outputs.tripped == 'true'", "env": {"FILES": "${{ steps.sensor.outputs.files }}"}, "run": "echo digest=x >>\"$GITHUB_OUTPUT\""}

acknowledge := {"if": "steps.sensor.outputs.tripped == 'true'", "run": "go run -C scripts/attestation . \\\n  -digest \"$DIGEST\" \\\n  -report r.md\n"}

challenge := {"id": "keys", "run": "go run -C scripts/security . challenge-keys -base \"$BASE\" -head \"$HEAD\" >>\"$GITHUB_OUTPUT\""}

ask := {"if": "steps.keys.outputs.asked == 'true'", "env": {"DIGEST": "${{ steps.keys.outputs.digest }}", "ANCHOR": "${{ steps.keys.outputs.anchor }}", "REPORT": "${{ steps.keys.outputs.report }}"}, "run": "go run -C scripts/attestation . \\\n  -topic keys \\\n  -digest \"$DIGEST\"\n"}

good := [sensor, digest, acknowledge, challenge, ask]

workflow(steps) := [{"path": ".github/workflows/sensitive-paths.yml", "contents": {"on": {"pull_request": null}, "jobs": {"tripwire": {"steps": steps}}}}]

replaced(i, step) := array.flatten([array.slice(good, 0, i), [step], array.slice(good, i + 1, count(good))])

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_the_gate_as_it_stands_passes if {
	count(sensitive_gate_labels.deny) == 0 with input as workflow(good)
}

test_a_label_read_or_written_is_refused if {
	denies(sensitive_gate_labels.deny, `reads labels ("--json labels")`) with input as workflow(array.concat(good, [{"run": "gh pr view \"$PR\" --json labels"}]))
}

test_a_labelled_trigger_is_refused if {
	i := [{"path": ".github/workflows/sensitive-paths.yml", "contents": {"true": {"pull_request": {"types": ["opened", "labeled"]}}, "jobs": {"tripwire": {"steps": good}}}}]
	some msg in sensitive_gate_labels.deny with input as i
	contains(msg, `("labeled")`)
}

test_no_workflow_is_refused if {
	denies(sensitive_gate_labels.deny, "is not there") with input as []
}
