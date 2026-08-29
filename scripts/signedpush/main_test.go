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
