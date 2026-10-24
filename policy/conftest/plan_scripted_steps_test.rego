package rules.plan_scripted_steps_test

import data.rules.plan_scripted_steps
import rego.v1

configured(provisioners) := {"path": "site-cluster.plan.json", "contents": {"configuration": {"root_module": {"module_calls": {"cluster": {"module": {"resources": [{"address": "terraform_data.step", "mode": "managed", "type": "terraform_data", "expressions": {}, "provisioners": provisioners}]}}}}}}}

step(interpreter) := {"type": "local-exec", "expressions": object.union({"command": {"references": ["path.module"]}}, interpreter)}

denies(i, words) if {
	some msg in plan_scripted_steps.deny with input as i
	contains(msg, words)
}

test_a_step_run_by_bash_passes if {
	count(plan_scripted_steps.deny) == 0 with input as [configured([step({"interpreter": {"constant_value": ["/bin/bash", "-c"]}})])]
}

test_a_step_left_to_sh_is_refused if {
	denies([configured([step({})])], "module.cluster.terraform_data.step: scripted steps do not name bash")
}

test_a_step_naming_sh_is_refused if {
	denies([configured([step({"interpreter": {"constant_value": ["/bin/sh", "-c"]}})])], "do not name bash as what runs them")
}

test_no_step_is_refused if {
	denies([configured([])], "checked nothing")
}
