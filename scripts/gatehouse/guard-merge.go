// What cannot be published - a merge commit on a branch that signedpush must
// replay.
//
// signedpush publishes by replaying a branch's commits as a linear chain,
// creating each through GitHub's Git Data API so GitHub signs it. A merge
// commit has two parents and cannot be replayed that way: flattening it would
// republish everything the merge brought in as new commits, so the branch
// would carry duplicates of history already on main and main would stop being
// an ancestor of it. signedpush therefore refuses, and says to rebase.
//
// It refuses at PUSH time, which is too late to be cheap. By then the merge
// commit exists, the full test suite has run on it, and clearing it means a
// rebase - which diverges the branch from its remote, which signedpush also
// refuses, which means deleting and recreating the ref, which closes the pull
// request (#260). One `git merge` costs a pull request.
//
// So the refusal moves to where the mistake is made. This runs at commit-msg,
// which git executes for merges - pre-commit is not run for them at all, which
// is why the stage matters.
//
// WHO THIS APPLIES TO, which is the question that got the push guard wrong
// once already. It is not "is this the agent". It is the same property
// question guard-push asks, one stage earlier: an UNSIGNED merge commit is the
// dead end, because only a party that cannot sign locally needs signedpush to
// publish it.
//
//   - The human has a user account, signs locally, and pushes with plain git.
//     A merge commit from them publishes perfectly well, and the Update branch
//     button on an epoch branch produces exactly one. Refusing that would
//     block the only party doing nothing wrong.
//   - The agent holds no user account by design, so it cannot sign locally at
//     all and must publish through signedpush, which cannot carry a merge.
//
// One honest difference from guard-push, since it changes what this can claim.
// guard-push inspects commits that exist and asks whether a signature is
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

func guardMerge(args []string) int {
	if !mergeInProgress() {
		return 0
	}
	if signsLocally() {
		return 0
	}

	fmt.Print(`gatehouse: this would be a merge commit, and it cannot be published.

signedpush replays a branch as a linear chain so GitHub can sign each commit,
and a merge commit has two parents. It would be refused at push time - after
the tests have run on it, and when clearing it costs a rebase, a recreated ref
and a closed pull request.

Take the rebase now instead:

    git merge --abort
    git fetch origin && git rebase origin/main

If you sign your own commits, this does not apply to you - set commit.gpgsign
and a plain push publishes a merge perfectly well.
`)
	return 1
}
