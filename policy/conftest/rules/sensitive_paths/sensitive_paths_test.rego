package rules.sensitive_paths_test

import data.rules.sensitive_paths
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
	count(sensitive_paths.deny) == 0 with input as workflow(good)
}

test_no_workflow_is_refused if {
	some msg in sensitive_paths.deny with input as []
	contains(msg, "is not there")
}

test_no_attestation_is_refused if {
	denies(sensitive_paths.deny, "does not run scripts/attestation") with input as workflow([sensor, digest, challenge])
}

test_an_attestation_with_no_digest_is_refused if {
	denies(sensitive_paths.deny, "not keyed to a digest") with input as workflow(replaced(2, object.union(acknowledge, {"run": "go run -C scripts/attestation . -report r.md"})))
}

test_the_review_thread_trigger_is_refused if {
	i := [{"path": ".github/workflows/sensitive-paths.yml", "contents": {"true": {"pull_request": null, "pull_request_review_thread": null}, "jobs": {"tripwire": {"steps": good}}}}]
	some msg in sensitive_paths.deny with input as i
	contains(msg, "pull_request_review_thread, which is not a workflow trigger")
}

test_no_digest_is_refused if {
	denies(sensitive_paths.deny, "nothing digests the sensitive part") with input as workflow([sensor, acknowledge, challenge, ask])
}

test_a_digest_of_the_whole_diff_is_refused if {
	denies(sensitive_paths.deny, "does not read the sensor's file list") with input as workflow(replaced(1, object.remove(digest, ["env"])))
}

test_a_step_that_may_fail_is_refused if {
	denies(sensitive_paths.deny, "the gate cannot refuse") with input as workflow(replaced(0, object.union(sensor, {"continue-on-error": true})))
}

test_an_unconditioned_acknowledgement_is_refused if {
	denies(sensitive_paths.deny, "not conditioned on the tripwire firing") with input as workflow(replaced(2, object.remove(acknowledge, ["if"])))
}

test_no_key_challenge_is_refused if {
	denies(sensitive_paths.deny, "no step runs security challenge-keys") with input as workflow([sensor, digest, acknowledge, ask])
}

test_a_conditioned_key_challenge_is_refused if {
	denies(sensitive_paths.deny, `runs only if "always()"`) with input as workflow(replaced(3, object.union(challenge, {"if": "always()"})))
}

test_a_key_challenge_with_no_id_is_refused if {
	denies(sensitive_paths.deny, "has no id") with input as workflow(replaced(3, object.remove(challenge, ["id"])))
}

test_one_conversation_for_both_is_refused if {
	denies(sensitive_paths.deny, "does not open two conversations") with input as workflow([sensor, digest, acknowledge, challenge])
}

test_an_ask_not_keyed_to_what_was_found_is_refused if {
	denies(sensitive_paths.deny, "not exactly when a key was handed out") with input as workflow(replaced(4, object.union(ask, {"if": "always()"})))
	denies(sensitive_paths.deny, "takes its DIGEST from") with input as workflow(replaced(4, object.union(ask, {"env": {"DIGEST": "${{ steps.digest.outputs.digest }}", "ANCHOR": "${{ steps.keys.outputs.anchor }}", "REPORT": "${{ steps.keys.outputs.report }}"}})))
}

test_the_same_topic_is_refused if {
	denies(sensitive_paths.deny, "under the same topic") with input as workflow(replaced(4, object.union(ask, {"run": "go run -C scripts/attestation . \\\n  -topic sensitive \\\n  -digest \"$DIGEST\"\n"})))
}

test_a_label_read_or_written_is_refused if {
	denies(sensitive_paths.deny, `reads labels ("--json labels")`) with input as workflow(array.concat(good, [{"run": "gh pr view \"$PR\" --json labels"}]))
}

test_a_labelled_trigger_is_refused if {
	i := [{"path": ".github/workflows/sensitive-paths.yml", "contents": {"true": {"pull_request": {"types": ["opened", "labeled"]}}, "jobs": {"tripwire": {"steps": good}}}}]
	some msg in sensitive_paths.deny with input as i
	contains(msg, `("labeled")`)
}
