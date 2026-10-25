package rules.hook_tasks_test

import data.rules.hook_tasks
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
	count(hook_tasks.deny) == 0 with input as [config([guard, validate]), tasks]
}

test_a_task_that_does_not_exist_is_refused if {
	denies(hook_tasks.deny, "runs `task validate`, and taskfile.yml has no such task") with input as [config([guard, validate]), taskfile({"push-guard": {}})]
}

test_an_internal_task_is_refused if {
	denies(hook_tasks.deny, "which is marked internal") with input as [config([guard, validate]), taskfile({"push-guard": {}, "validate": {"internal": true}})]
}

test_no_task_invoked_is_refused if {
	denies(hook_tasks.deny, "no hook invokes a task") with input as [config([object.union(guard, {"entry": "./guard.sh"})]), tasks]
}
