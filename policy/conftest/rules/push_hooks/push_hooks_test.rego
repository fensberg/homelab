package rules.push_hooks_test

import data.rules.push_hooks
import rego.v1

config(hooks) := {"path": ".pre-commit-config.yaml", "contents": {"repos": [{"repo": "local", "hooks": hooks}]}}

taskfile(tasks) := {"path": "taskfile.yml", "contents": {"tasks": tasks}}

guard := {"id": "enforce-push", "entry": "task push-guard", "stages": ["pre-push"], "fail_fast": true}

validate := {"id": "validate", "entry": "task validate", "stages": ["pre-push"]}

tasks := taskfile({"push-guard": {"cmds": ["x"]}, "validate": {"cmds": ["y"]}})

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_the_guard_first_and_every_task_callable_passes if {
	count(push_hooks.deny) == 0 with input as [config([guard, validate]), tasks]
}

test_no_guard_is_refused if {
	denies(push_hooks.deny, `no "enforce-push" hook`) with input as [config([validate]), tasks]
}

test_a_guard_at_another_stage_is_refused if {
	denies(push_hooks.deny, "it only guards anything at pre-push") with input as [config([object.union(guard, {"stages": ["pre-commit"]}), validate]), tasks]
}

test_a_guard_that_is_not_fail_fast_is_refused if {
	denies(push_hooks.deny, "is not fail_fast") with input as [config([object.union(guard, {"fail_fast": false}), validate]), tasks]
}

test_a_guard_after_another_pre_push_hook_is_refused if {
	denies(push_hooks.deny, "should be first") with input as [config([validate, guard]), tasks]
}

test_a_hook_configuration_of_nothing_is_refused if {
	denies(push_hooks.deny, "the hook configuration parsed to nothing") with input as [config([]), tasks]
}
