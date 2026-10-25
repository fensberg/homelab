package rules.sensitive_gate_refuses_test

import data.rules.sensitive_gate_refuses
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
	count(sensitive_gate_refuses.deny) == 0 with input as workflow(good)
}

test_a_step_that_may_fail_is_refused if {
	denies(sensitive_gate_refuses.deny, "the gate cannot refuse") with input as workflow(replaced(0, object.union(sensor, {"continue-on-error": true})))
}

test_an_unconditioned_acknowledgement_is_refused if {
	denies(sensitive_gate_refuses.deny, "not conditioned on the tripwire firing") with input as workflow(replaced(2, object.remove(acknowledge, ["if"])))
}

test_no_workflow_is_refused if {
	denies(sensitive_gate_refuses.deny, "is not there") with input as []
}
