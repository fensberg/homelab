# Every `task` a hook invokes has to exist and be callable from outside.
#
# This was once a check that named push-guard alone (rules.push_hooks keeps
# that hook wired), which is how it stayed green while a second hook called a
# task at all: the assertion was about one hook rather than about the
# property, so the property went unenforced the moment a second case
# appeared. `internal: true` is the specific failure - task refuses to run
# those from the CLI, so the hook fails with a task error rather than a
# verdict, which is a guard broken in the direction of noise.
package rules.hook_tasks

import data.lib
import rego.v1

config := [f | some f in lib.at(".pre-commit-config.yaml")]

hooks := [hook | some f in config; some repo in object.get(f.contents, "repos", []); some hook in object.get(repo, "hooks", [])]

tasks := object.get([f | some f in lib.at("taskfile.yml")][0].contents, "tasks", {})

invoked contains [hook.id, fields[1]] if {
	some hook in hooks
	fields := regex.split(`\s+`, trim_space(object.get(hook, "entry", "")))
	count(fields) >= 2
	fields[0] == "task"
}

deny contains msg if {
	some [id, name] in invoked
	not tasks[name]
	msg := $"hook \"{id}\" runs `task {name}`, and taskfile.yml has no such task"
}

deny contains msg if {
	some [id, name] in invoked
	tasks[name].internal == true
	msg := $"hook \"{id}\" runs `task {name}`, which is marked internal - task refuses to run an internal task from the CLI, so the hook fails with a task error instead of a verdict"
}

deny contains msg if {
	count(invoked) == 0
	msg := "no hook invokes a task, so this checked nothing. Either the hooks stopped using task or the entry is no longer written as `task <name>`."
}
