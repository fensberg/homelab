package rules.scripted_steps_test

import data.rules.scripted_steps
import rego.v1

step(script) := [{"path": "modules/c/cilium.tf", "contents": {"resource": {"terraform_data": {"cilium": [{"provisioner": {"local-exec": [script]}}]}}}}]

denies(script, words) if {
	some msg in scripted_steps.deny with input as step(script)
	contains(msg, words)
}

test_a_bash_step_that_says_so_passes if {
	count(scripted_steps.deny) == 0 with input as step({"command": "set -euo pipefail\necho", "interpreter": ["/bin/bash", "-c"]})
}

test_a_bash_step_left_to_sh_is_refused if {
	denies({"command": "set -euo pipefail\necho"}, "do not name bash")
	denies({"command": "set -euo pipefail\necho", "interpreter": ["/bin/sh", "-c"]}, "do not name bash")
}

test_a_plain_step_needs_nothing if {
	count(scripted_steps.deny) == 0 with input as step({"command": "echo done"})
}

test_no_step_at_all_is_refused if {
	some msg in scripted_steps.deny with input as [{"path": "a.tf", "contents": {}}]
	contains(msg, "no scripted step was found")
}
