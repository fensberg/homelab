package main

import (
	"fmt"
	"homelab/details/gitenv"
	"os/exec"
	"strings"
	"testing"
)

// mergesOfMain shells out to git, so it is exercised against a real throwaway
// repository rather than a stub. The bug it guards against was invisible in
// this program's output and only obvious in the log days later, which is
// exactly the kind that needs a test rather than care.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	run("config", "commit.gpgsign", "false")
	run("commit", "-q", "--allow-empty", "-m", "base")
	return dir
}

func inDir(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// conflicted is a branch one commit ahead of where it left main, with main
// moved on by one commit since: the state a branch is in when it has to be
// brought up to main. It returns main's commit.
func conflicted(t *testing.T, dir string) string {
	t.Helper()
	inDir(t, dir, "checkout", "-q", "-b", "piece")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "the piece's own work")
	inDir(t, dir, "checkout", "-q", "main")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "somebody else's, merged to main")
	onMain := inDir(t, dir, "rev-parse", "HEAD")
	inDir(t, dir, "update-ref", gitenv.RemoteMain, onMain)
	inDir(t, dir, "checkout", "-q", "piece")
	return onMain
}

func TestALinearBranchHasNoMerges(t *testing.T) {
	dir := gitRepo(t)
	t.Chdir(dir)
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "two")

	revs := inDir(t, dir, "rev-list", "HEAD~2..HEAD")
	if merges, err := mergesOfMain(strings.Fields(revs)); err != nil || len(merges) != 0 {
		t.Errorf("a linear branch must publish as it is: %v, %v", merges, err)
	}
}

// A merge of main is published as a merge: one signed commit with two
// parents, the branch's previous signed commit and main's commit as it is.
// Nothing main already holds is made again, which is what flattening a merge
// did, and what refusing every merge was protecting against.
func TestAMergeOfMainIsPublishedAsAMergeAndMainIsNotMadeAgain(t *testing.T) {
	dir := gitRepo(t)
	t.Chdir(dir)
	base := inDir(t, dir, "rev-parse", "HEAD")
	onMain := conflicted(t, dir)
	inDir(t, dir, "merge", "-q", "--no-ff", "-m", "bring the piece up to main", "main")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "more of the piece's work")

	commits := strings.Fields(inDir(t, dir, "rev-list", "--reverse", "--first-parent", base+"..HEAD"))
	if len(commits) != 3 {
		t.Fatalf("the piece's own commits are %d, not its two and the merge: main's were counted as its own", len(commits))
	}
	merges, err := mergesOfMain(commits)
	if err != nil {
		t.Fatalf("a merge of main must publish: %v", err)
	}
	if len(merges) != 1 || merges[commits[1]] != onMain {
		t.Fatalf("the merge is said to bring in %v, not main's commit %s", merges, onMain)
	}

	var made [][]string
	var said []string
	last, err := replay(commits, base, merges, func(message, _ string, parents []string) (string, error) {
		made = append(made, parents)
		said = append(said, message)
		return fmt.Sprintf("signed-%d-000000", len(made)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{base}, {"signed-1-000000", onMain}, {"signed-2-000000"}}
	if fmt.Sprint(made) != fmt.Sprint(want) || last != "signed-3-000000" {
		t.Errorf("the commits were made on\n  %v\nnot on\n  %v", made, want)
	}
	for _, message := range said {
		if strings.Contains(message, "somebody else's") {
			t.Error("a commit main already holds was made again, which is the duplicate history a flattened merge leaves")
		}
	}
}

// A merge of anything but main is another branch's work arriving in this
// one, and is refused with what it brought in named.
func TestAMergeOfAnythingButMainIsRefused(t *testing.T) {
	dir := gitRepo(t)
	t.Chdir(dir)
	base := inDir(t, dir, "rev-parse", "HEAD")
	conflicted(t, dir)
	inDir(t, dir, "checkout", "-q", "-b", "another", base)
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "another piece's work")
	inDir(t, dir, "checkout", "-q", "piece")
	inDir(t, dir, "merge", "-q", "--no-ff", "-m", "take the other piece's work", "another")

	commits := strings.Fields(inDir(t, dir, "rev-list", "--reverse", "--first-parent", base+"..HEAD"))
	_, err := mergesOfMain(commits)
	if err == nil || !strings.Contains(err.Error(), "not on main") {
		t.Fatalf("a merge of another branch was let through, or refused for another reason: %v", err)
	}

	inDir(t, dir, "checkout", "-q", "-b", "third", base)
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "a third piece's work")
	inDir(t, dir, "checkout", "-q", "-b", "all-at-once", base)
	inDir(t, dir, "merge", "-q", "--no-ff", "-m", "three histories", "main", "third")
	commits = strings.Fields(inDir(t, dir, "rev-list", "--first-parent", base+"..HEAD"))
	if _, err := mergesOfMain(commits); err == nil || !strings.Contains(err.Error(), "more than two") {
		t.Errorf("a merge of three histories was let through, or refused for another reason: %v", err)
	}
}

// --- a branch must carry only its own work -----------------------------------

// repoWithRemote gives the fixture real remote-tracking refs, because the
// stacking check reads them: "is this commit also on another remote branch"
// cannot be answered against a repository that has no remotes.
func repoWithRemote(t *testing.T) string {
	t.Helper()
	bare := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", bare).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v\n%s", err, out)
	}
	dir := gitRepo(t)
	inDir(t, dir, "remote", "add", "origin", bare)
	inDir(t, dir, "push", "-q", "origin", "main")
	return dir
}

func TestRefuseStackedAllowsABranchCutFromMain(t *testing.T) {
	dir := repoWithRemote(t)
	t.Chdir(dir)
	inDir(t, dir, "checkout", "-q", "-b", "fix/mine", "origin/main")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "my work")

	if err := refuseStacked("fix/mine"); err != nil {
		t.Errorf("a branch cut from main must publish: %v", err)
	}
}

// The mistake this exists for: a branch cut while another feature branch was
// checked out, inheriting its commits. Invisible at the time - the branch
// looks fine and the tests pass - and it surfaces as a conflict on every
// shared file once the other branch is squash-merged.
func TestRefuseStackedRejectsABranchBuiltOnAnotherFeatureBranch(t *testing.T) {
	dir := repoWithRemote(t)
	t.Chdir(dir)
	inDir(t, dir, "checkout", "-q", "-b", "fix/first", "origin/main")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "the other branch's work")
	inDir(t, dir, "push", "-q", "origin", "fix/first")

	inDir(t, dir, "checkout", "-q", "-b", "fix/second")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "my work")

	err := refuseStacked("fix/second")
	if err == nil {
		t.Fatal("a stacked branch published: when fix/first is squash-merged this conflicts on every file they share, and the person who sees it did nothing wrong")
	}
	for _, want := range []string{"fix/first", "stacked", "rebase --onto origin/main"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q, so it names the problem without the remedy:\n%s", want, err)
		}
	}
}

// The estate's own model must survive the guard. Pieces of an epoch branch
// from it and target it, so they carry its commits by design - and the push
// guard was shipped once refusing exactly the party doing nothing wrong.
func TestRefuseStackedAllowsABranchBasedOnAnEpochBranch(t *testing.T) {
	dir := repoWithRemote(t)
	t.Chdir(dir)
	inDir(t, dir, "checkout", "-q", "-b", "epoch/02-abstraction", "origin/main")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "epoch work")
	inDir(t, dir, "push", "-q", "origin", "epoch/02-abstraction")

	inDir(t, dir, "checkout", "-q", "-b", "feat/a-piece-of-the-epoch")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "my piece")

	if err := refuseStacked("feat/a-piece-of-the-epoch"); err != nil {
		t.Errorf("a branch based on an epoch branch is the documented way of working and must publish: %v", err)
	}
}

// Two pieces of one epoch, open at the same time, must both publish.
//
// This is the case the exemption missed. Both are cut from the epoch branch, so
// both carry its commits - and each then looks, to the other, like a branch
// stacked on a plain feature branch. The guard asked whether the branch it was
// comparing against was an epoch branch, when what it needed to ask was whether
// the COMMIT belonged to one.
//
// The symptom was that the second piece could not publish until the first
// merged, which is not a rule anybody chose and which arrived as a refusal
// naming the epoch's own tip.
func TestRefuseStackedAllowsASecondPieceOfTheSameEpoch(t *testing.T) {
	dir := repoWithRemote(t)
	t.Chdir(dir)
	inDir(t, dir, "checkout", "-q", "-b", "epoch/03-workload", "origin/main")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "epoch work")
	inDir(t, dir, "push", "-q", "origin", "epoch/03-workload")

	// The first piece, published and still open.
	inDir(t, dir, "checkout", "-q", "-b", "feat/first-piece")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "first piece")
	inDir(t, dir, "push", "-q", "origin", "feat/first-piece")

	// The second, cut from the epoch branch exactly as the first was.
	inDir(t, dir, "checkout", "-q", "-B", "feat/second-piece", "epoch/03-workload")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "second piece")

	if err := refuseStacked("feat/second-piece"); err != nil {
		t.Errorf("a second piece of one epoch cannot publish while the first is open: %v\n\n"+
			"Both carry the epoch's commits because both were cut from it, which is the "+
			"documented way of working. Serialising pieces behind each other's merges is "+
			"not a rule anybody chose.", err)
	}
}

// And the guard still catches what it was written for: a branch carrying
// another feature branch's work, where the shared commit belongs to no epoch.
func TestRefuseStackedStillRejectsARealStackOutsideAnEpoch(t *testing.T) {
	dir := repoWithRemote(t)
	t.Chdir(dir)
	inDir(t, dir, "checkout", "-q", "-b", "feat/first", "origin/main")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "first branch work")
	inDir(t, dir, "push", "-q", "origin", "feat/first")

	inDir(t, dir, "checkout", "-q", "-b", "feat/second")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "second branch work")

	if err := refuseStacked("feat/second"); err == nil {
		t.Error("a branch built on another feature branch published.\n\n" +
			"That is the case this guard exists for: when the first is squash-merged, its " +
			"commit becomes a different one with the same content, and merging the second " +
			"conflicts on every file they both touch.")
	}
}

// A guard that cannot see the range must say so rather than pass.
func TestRefuseStackedRefusesWhenItCannotSeeMain(t *testing.T) {
	dir := gitRepo(t) // no remote at all, so no origin/main
	t.Chdir(dir)

	err := refuseStacked("fix/whatever")
	if err == nil {
		t.Fatal("with no origin/main there is no range to check, and passing would be a guard that silently checks nothing")
	}
	if !strings.Contains(err.Error(), "origin/main") {
		t.Errorf("the refusal does not name what is missing: %v", err)
	}
}

// A failing git command says what git said.
//
// This is not a style preference. The stderr used to be dropped, so a rejected
// push reported "exit status 1" and nothing else - and the message git had
// printed named the cause exactly. A wrapper that swallows a subprocess's
// diagnosis converts a one-line answer into an investigation, every time,
// for everybody.
func TestAFailingGitCommandCarriesWhatGitSaid(t *testing.T) {
	// Neutralised so this reports the code rather than whatever git
	// configuration the machine happens to carry.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")

	dir := gitRepo(t)
	t.Chdir(dir)

	_, err := git("rev-parse", "--verify", "a-ref-that-does-not-exist")
	if err == nil {
		t.Fatal("resolving a ref that does not exist succeeded, so this asserts nothing")
	}
	if !strings.Contains(err.Error(), "fatal") {
		t.Errorf(`the error carries no line from git, so a caller sees only an exit status:

%v

git printed something on stderr and it was dropped. That is what turned a
rejected push into four wrong theories.`, err)
	}
}

// A piece of an epoch signs its own commits and leaves the epoch's alone.
//
// THE FAILURE THIS REPRODUCES (#342). The base used to be
// merge-base(origin/main, HEAD), so every commit the epoch branch had added sat
// above it and was re-signed - identical content, new SHAs. Git's merge base
// between the piece and the epoch branch then dropped to main, and any change
// the piece made to lines those commits introduced read as a competing edit.
// #340 conflicted in three files against a base that already contained exactly
// the change it was built on.
func TestAPieceOfAnEpochDoesNotResignTheEpochsCommits(t *testing.T) {
	dir := repoWithRemote(t)
	t.Chdir(dir)

	inDir(t, dir, "checkout", "-q", "-b", "epoch/03-workload", "origin/main")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "epoch: the first piece")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "epoch: the second piece")
	inDir(t, dir, "push", "-q", "origin", "epoch/03-workload")
	epochTip := inDir(t, dir, "rev-parse", "HEAD")

	inDir(t, dir, "checkout", "-q", "-b", "feat/third-piece")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "the piece's own work")

	base, err := baseForNewBranch()
	if err != nil {
		t.Fatalf("baseForNewBranch: %v", err)
	}
	if base != epochTip {
		t.Fatalf(`base is %s, want the epoch tip %s.

Anything above the base is re-signed, so an earlier base means the epoch
branch's own commits arrive on GitHub with new SHAs. The merge base between
this piece and the epoch branch then drops to main, and the pull request
conflicts in files nobody disagreed about.`, base[:8], epochTip[:8])
	}

	// And the consequence, stated as the thing that actually matters: exactly
	// one commit gets signed.
	revs := inDir(t, dir, "rev-list", "--count", base+"..HEAD")
	if revs != "1" {
		t.Errorf("%s commit(s) would be signed; only the piece's own should be", revs)
	}
}

// A branch cut from main still starts at main.
//
// The converse, because a base that is always HEAD would pass the test above
// and publish nothing at all.
func TestABranchCutFromMainStartsAtMain(t *testing.T) {
	dir := repoWithRemote(t)
	t.Chdir(dir)
	mainTip := inDir(t, dir, "rev-parse", "origin/main")

	inDir(t, dir, "checkout", "-q", "-b", "fix/mine", "origin/main")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "two")

	base, err := baseForNewBranch()
	if err != nil {
		t.Fatalf("baseForNewBranch: %v", err)
	}
	if base != mainTip {
		t.Errorf("base is %s, want main's tip %s", base[:8], mainTip[:8])
	}
	if revs := inDir(t, dir, "rev-list", "--count", base+"..HEAD"); revs != "2" {
		t.Errorf("%s commit(s) would be signed; both of this branch's should be", revs)
	}
}

// A branch whose commits are all already published signs nothing.
//
// Reaching for a parent there would sign a commit the remote already holds
// under a different ref, which is the same identity change the epoch case is
// about, arriving from the other direction.
func TestABranchWithNothingNewSignsNothing(t *testing.T) {
	dir := repoWithRemote(t)
	t.Chdir(dir)
	inDir(t, dir, "checkout", "-q", "-b", "epoch/04-observability", "origin/main")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "epoch work")
	inDir(t, dir, "push", "-q", "origin", "epoch/04-observability")

	inDir(t, dir, "checkout", "-q", "-b", "feat/nothing-yet")

	base, err := baseForNewBranch()
	if err != nil {
		t.Fatalf("baseForNewBranch: %v", err)
	}
	if base != "HEAD" {
		t.Errorf("base is %q, want HEAD - there is nothing unpublished to sign", base)
	}
}
