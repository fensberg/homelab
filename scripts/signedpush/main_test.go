package main

import (
	"os/exec"
	"strings"
	"testing"
)

// refuseMerges shells out to git, so it is exercised against a real throwaway
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

func TestRefuseMergesAcceptsALinearBranch(t *testing.T) {
	dir := gitRepo(t)
	t.Chdir(dir)
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "two")

	revs := inDir(t, dir, "rev-list", "HEAD~2..HEAD")
	if err := refuseMerges(strings.Fields(revs)); err != nil {
		t.Errorf("a linear branch must publish: %v", err)
	}
}

func TestRefuseMergesRejectsAMergeCommit(t *testing.T) {
	dir := gitRepo(t)
	t.Chdir(dir)
	base := inDir(t, dir, "rev-parse", "HEAD")
	inDir(t, dir, "checkout", "-q", "-b", "side")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "side work")
	inDir(t, dir, "checkout", "-q", "main")
	inDir(t, dir, "commit", "-q", "--allow-empty", "-m", "main work")
	inDir(t, dir, "merge", "-q", "--no-ff", "-m", "Merge side", "side")

	revs := inDir(t, dir, "rev-list", base+"..HEAD")
	err := refuseMerges(strings.Fields(revs))
	if err == nil {
		t.Fatal("a merge commit must be refused - flattening it silently replicates history")
	}
	if !strings.Contains(err.Error(), "rebase") {
		t.Errorf("the error should name the way out, got: %v", err)
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
