# Every action's commit is written in exactly one place.
#
# harden-runner was once pinned in 31 jobs and checkout in 30. They agreed,
# and nothing made them: a second `uses:` naming an action that already has
# one is a second place to update, which can name a different commit with
# nothing to notice. So the second copy is the failure, whatever it names.
# The one literal lives in a composite action under .github/workflows,
# referenced everywhere else with $/.
#
# Per action path, and one commit per repository: two paths of one
# repository may each appear once, but must name the same commit.
#
# Whether a reference is pinned at all is zizmor's (unpinned-uses).
package action_commits

import data.lib
import rego.v1

pinned := `^([A-Za-z0-9._-]+/[A-Za-z0-9._-]+)((?:/[^@]+)?)@([0-9a-f]{40})$`

sites contains [path, repo, commit, file, where] if {
	some [file, where, ref] in lib.uses
	m := regex.find_all_string_submatch_n(pinned, ref, 1)[0]
	repo := m[1]
	path := concat("", [m[1], m[2]])
	commit := m[3]
}

deny contains msg if {
	some path in {p | some [p, _, _, _, _] in sites}
	written := [[file, commit] | some [p, _, commit, file, _] in sites; p == path]
	count(written) > 1
	where := concat("\n  ", sort([sprintf("%s  @%s", [file, substring(commit, 0, 12)]) | some [file, commit] in written]))
	msg := sprintf("%s is written in %d places:\n  %s\nAn action's commit may be written once. Reference the shared composite action with $/ instead, or add one under .github/workflows/<name>/action.yml holding the only literal.", [path, count(written), where])
}

deny contains msg if {
	some repo in {r | some [_, r, _, _, _] in sites}
	commits := {commit | some [_, r, commit, _, _] in sites; r == repo}
	count(commits) > 1
	detail := concat("\n  ", sort([sprintf("%s  %s", [commit, concat(", ", sort({p | some [p, r, c, _, _] in sites; r == repo; c == commit}))]) | some commit in commits]))
	msg := sprintf("%s is pinned to %d different commits across its action paths:\n  %s\nA commit belongs to the repository, not to a path inside it.", [repo, count(commits), detail])
}

# A reading that found nothing would pass both rules. These are the smallest
# numbers that could describe this repository at all.
deny contains msg if {
	count(lib.runnable) < 10
	msg := sprintf("only %d workflow or action file(s) were read, so the commit rule checked almost nothing", [count(lib.runnable)])
}

deny contains msg if {
	count({p | some [p, _, _, _, _] in sites}) < 10
	msg := "fewer than 10 pinned action paths were found; the reading has stopped matching"
}

deny contains msg if {
	count({ref | some [_, _, ref] in lib.uses; startswith(ref, "$/")}) == 0
	msg := "no $/ reference was found anywhere. Every job starts with $/.github/workflows/mobilize, so finding none means the reading is wrong, or the shared actions are gone and every copy is back."
}
