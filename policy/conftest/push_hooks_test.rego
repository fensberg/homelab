package push_hooks_test

import data.push_hooks
import rego.v1

config(hooks) := {"path": ".pre-commit-config.yaml", "contents": {"repos": [{"repo": "local", "hooks": hooks}]}}

taskfile(tasks) := {"path": "taskfile.yml", "contents": {"tasks": tasks}}

guard := {"id": "enforce-push", "entry": "task push-guard", "stages": ["pre-push"], "fail_fast": true}

validate := {"id": "validate", "entry": "task validate", "stages": ["pre-push"]}

tasks := taskfile({"push-guard": {"cmds": ["x"]}, "validate": {"cmds": ["y"]}})

denies(hooks, t, words) if {
	some msg in push_hooks.deny with input as [config(hooks), t]
	contains(msg, words)
}

test_the_guard_first_and_every_task_callable_passes if {
	count(push_hooks.deny) == 0 with input as [config([guard, validate]), tasks]
}

test_no_guard_is_refused if {
	denies([validate], tasks, `no "enforce-push" hook`)
}

test_a_guard_at_another_stage_is_refused if {
	denies([object.union(guard, {"stages": ["pre-commit"]}), validate], tasks, "it only guards anything at pre-push")
}

test_a_guard_that_is_not_fail_fast_is_refused if {
	denies([object.union(guard, {"fail_fast": false}), validate], tasks, "is not fail_fast")
}

test_a_guard_after_another_pre_push_hook_is_refused if {
	denies([validate, guard], tasks, "should be first")
}

test_a_task_that_does_not_exist_is_refused if {
	denies([guard, validate], taskfile({"push-guard": {}}), "runs `task validate`, and taskfile.yml has no such task")
}

test_an_internal_task_is_refused if {
	denies([guard, validate], taskfile({"push-guard": {}, "validate": {"internal": true}}), "which is marked internal")
}

test_no_task_invoked_is_refused if {
	denies([object.union(guard, {"entry": "./guard.sh"})], tasks, "no hook invokes a task")
}
