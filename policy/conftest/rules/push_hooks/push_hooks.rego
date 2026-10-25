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
# That every task a hook invokes can be run is rules.hook_tasks.
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
	msg := $"no \"{guard}\" hook in .pre-commit-config.yaml. A plain `git push` produces unsigned commits that nobody can rewrite afterwards, because non_fast_forward applies to feature branches here. Publish with `task push`; do not remove the hook that enforces it."
}

deny contains msg if {
	some hook in the_guard
	object.get(hook, "stages", []) != ["pre-push"]
	msg := $"{guard} runs at stages {object.get(hook, "stages", [])}; it only guards anything at pre-push"
}

deny contains msg if {
	msg := $"{guard} is not fail_fast, so a refused push still runs validate and test before printing the refusal"
	some hook in the_guard
	object.get(hook, "fail_fast", false) != true
}

deny contains msg if {
	count(pre_push) > 0
	pre_push[0] != guard
	msg := $"pre-push hooks run in order {pre_push}; {guard} should be first so a doomed push fails fast instead of after validate and test"
}
