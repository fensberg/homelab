// What cannot be published - a merge commit signedpush will not replay.
//
// signedpush publishes by replaying a branch's commits, creating each through
// GitHub's Git Data API so GitHub signs it. It replays one kind of merge: main
// brought into a branch, which is how a conflict with main is cleared without
// rewriting anything. A merge of anything else is another branch's work
// arriving in this one, and signedpush refuses it.
//
// It refuses at PUSH time, which is too late to be cheap. By then the merge
// commit exists and the full test suite has run on it. So the refusal is also
// here, where the mistake is made. This runs at commit-msg, which git executes
// for merges - pre-commit is not run for them at all, which is why the stage
// matters.
//
// WHO THIS APPLIES TO, which is the question that got the push guard wrong
// once already. It is not "is this the agent". It is the same property
// question enforce-push asks, one stage earlier: an UNSIGNED merge commit is
// the one that has to go through signedpush, because only a party that cannot
// sign locally needs it to publish.
//
//   - The human has a user account, signs locally, and pushes with plain git.
//     Any merge commit from them publishes perfectly well. Refusing that would
//     block the only party doing nothing wrong.
//   - The agent holds no user account by design, so it cannot sign locally at
//     all and must publish through signedpush, which carries a merge of main
//     and no other.
//
// One honest difference from enforce-push, since it changes what this can claim.
// enforce-push inspects commits that exist and asks whether a signature is
// present. At commit-msg time the commit does not exist yet, so this has to
// predict from `commit.gpgsign`. That is not an escape hatch - setting it true
// without a working key makes `git commit` fail outright rather than pass this
// - but it is a prediction rather than an observation, and worth saying so.
package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// mergeInProgress is true while git is assembling a merge commit.
//
// MERGE_HEAD exists from the moment `git merge` cannot fast-forward until the
// commit is made, and it covers both routes to one: `git merge` committing for
// itself, and `git merge --no-commit` followed by `git commit`. Cherry-pick
// and revert set their own heads and are not merges, so they are unaffected.
func mergeInProgress() bool {
	return exec.Command("git", "rev-parse", "-q", "--verify", "MERGE_HEAD").Run() == nil
}

// signsLocally reports whether this committer's commits carry a signature.
func signsLocally() bool {
	out, err := exec.Command("git", "config", "--get", "commit.gpgsign").Output()
	if err != nil {
		// Unset is the common case and git exits non-zero for it. Absent
		// configuration means unsigned, which is the answer that leads to the
		// refusal rather than around it.
		return false
	}
	return strings.EqualFold(strings.TrimSpace(string(out)), "true")
}

// mergesMain reports whether the merge being made brings in main and nothing
// else: what is being merged is a commit origin/main already holds.
func mergesMain() bool {
	return exec.Command("git", "merge-base", "--is-ancestor", "MERGE_HEAD", "origin/main").Run() == nil
}

func enforceMerge(args []string) int {
	if !mergeInProgress() {
		return 0
	}
	if signsLocally() {
		return 0
	}
	// The one merge signedpush publishes: main, brought into a branch to
	// clear a conflict with it.
	if mergesMain() {
		return 0
	}

	fmt.Print(`superintendent: this would be a merge commit, and it cannot be published.

signedpush publishes one kind of merge: main, brought into a branch. What is
being merged here is not a commit of origin/main, so it is another branch's
work, and it would be refused at push time - after the tests have run on it.

Stop now instead:

    git merge --abort

If it is main you meant, and this clone is behind it:

    git fetch origin && git merge origin/main

If you sign your own commits, this does not apply to you - set commit.gpgsign
and a plain push publishes a merge perfectly well.
`)
	return 1
}
