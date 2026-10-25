# A plain `git push` must stay refused.
#
# A GitHub App cannot hold a signing key, so commits are signed by GitHub on
# the App's behalf through the Git Data API - which is what `task push`
# does. A plain `git push` also works, and produces unsigned commits
# attributed to whatever local git config happens to say.
#
# That was documented in CLAUDE.md, in the taskfile and in signedpush's own
# header, and documentation was not enough: a session pushed twice with plain
# git before anyone noticed. The commits could not be repaired afterwards
# because `non_fast_forward` applies to feature branches here too, so there
# is no force-push available to anybody, agent or admin. The only remedy was
# a new branch and a new pull request.
#
# So the rule is enforced by a pre-push hook, and this rule is what keeps the
# hook wired. Deleting the hook to make a push go through is exactly the
# shortcut this exists to catch.
#
# Every `task` a hook invokes has to exist and be callable from outside.
#
# This named push-guard alone, which is how it stayed green while a second
# hook called a task at all: the assertion was about one hook rather than
# about the property, so the property went unenforced the moment a second
# case appeared. `internal: true` is the specific failure - task refuses to
# run those from the CLI, so the hook fails with a task error rather than a
# verdict, which is a guard broken in the direction of noise.
package rules.push_hooks

import data.lib
import rego.v1

guard := "enforce-push"

config := [f | some f in lib.at(".pre-commit-config.yaml")]

hooks := [hook | some f in config; some repo in object.get(f.contents, "repos", []); some hook in object.get(repo, "hooks", [])]

pre_push := [hook.id | some hook in hooks; "pre-push" in object.get(hook, "stages", [])]

the_guard := [hook | some hook in hooks; hook.id == guard]

deny contains msg if {
	count(hooks) == 0
	msg := "the hook configuration parsed to nothing, so every rule reading it proves nothing"
}

deny contains msg if {
	count(hooks) > 0
	count(the_guard) == 0
	msg := sprintf("no %q hook in .pre-commit-config.yaml. A plain `git push` produces unsigned commits that nobody can rewrite afterwards, because non_fast_forward applies to feature branches here. Publish with `task push`; do not remove the hook that enforces it.", [guard])
}

deny contains msg if {
	some hook in the_guard
	object.get(hook, "stages", []) != ["pre-push"]
	msg := sprintf("%s runs at stages %v; it only guards anything at pre-push", [guard, object.get(hook, "stages", [])])
}

deny contains msg if {
	some hook in the_guard
	object.get(hook, "fail_fast", false) != true
	msg := sprintf("%s is not fail_fast, so a refused push still runs validate and test before printing the refusal", [guard])
}

deny contains msg if {
	count(pre_push) > 0
	pre_push[0] != guard
	msg := sprintf("pre-push hooks run in order %v; %s should be first so a doomed push fails fast instead of after validate and test", [pre_push, guard])
}

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
	msg := sprintf("hook %q runs `task %s`, and taskfile.yml has no such task", [id, name])
}

deny contains msg if {
	some [id, name] in invoked
	tasks[name].internal == true
	msg := sprintf("hook %q runs `task %s`, which is marked internal - task refuses to run an internal task from the CLI, so the hook fails with a task error instead of a verdict", [id, name])
}

deny contains msg if {
	count(invoked) == 0
	msg := "no hook invokes a task, so this checked nothing. Either the hooks stopped using task or the entry is no longer written as `task <name>`."
}
