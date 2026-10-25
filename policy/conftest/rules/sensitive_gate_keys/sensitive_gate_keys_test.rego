package rules.sensitive_gate_keys_test

import data.rules.sensitive_gate_keys
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
	count(sensitive_gate_keys.deny) == 0 with input as workflow(good)
}

test_no_key_challenge_is_refused if {
	denies(sensitive_gate_keys.deny, "no step runs security challenge-keys") with input as workflow([sensor, digest, acknowledge, ask])
}

test_a_conditioned_key_challenge_is_refused if {
	denies(sensitive_gate_keys.deny, `runs only if "always()"`) with input as workflow(replaced(3, object.union(challenge, {"if": "always()"})))
}

test_a_key_challenge_with_no_id_is_refused if {
	denies(sensitive_gate_keys.deny, "has no id") with input as workflow(replaced(3, object.remove(challenge, ["id"])))
}

test_one_conversation_for_both_is_refused if {
	denies(sensitive_gate_keys.deny, "does not open two conversations") with input as workflow([sensor, digest, acknowledge, challenge])
}

test_an_ask_not_keyed_to_what_was_found_is_refused if {
	denies(sensitive_gate_keys.deny, "not exactly when a key was handed out") with input as workflow(replaced(4, object.union(ask, {"if": "always()"})))
	denies(sensitive_gate_keys.deny, "takes its DIGEST from") with input as workflow(replaced(4, object.union(ask, {"env": {"DIGEST": "${{ steps.digest.outputs.digest }}", "ANCHOR": "${{ steps.keys.outputs.anchor }}", "REPORT": "${{ steps.keys.outputs.report }}"}})))
}

test_the_same_topic_is_refused if {
	denies(sensitive_gate_keys.deny, "under the same topic") with input as workflow(replaced(4, object.union(ask, {"run": "go run -C scripts/attestation . \\\n  -topic sensitive \\\n  -digest \"$DIGEST\"\n"})))
}

test_no_workflow_is_refused if {
	denies(sensitive_gate_keys.deny, "is not there") with input as []
}
