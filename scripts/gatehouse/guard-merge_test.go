package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A real repository, because the guard reads git state rather than arguments.
//
// Two commits on divergent branches so that a merge cannot fast-forward - a
// fast-forward makes no merge commit, sets no MERGE_HEAD, and would have the
// test pass for the wrong reason.
func repoMidMerge(t *testing.T, sign bool) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "-q", "-b", "main")
	write("base", "base\n")
	run("add", ".")
	run("commit", "-qm", "base")
	run("checkout", "-q", "-b", "side")
	write("side", "side\n")
	run("add", ".")
	run("commit", "-qm", "side")
	run("checkout", "-q", "main")
	write("main", "main\n")
	run("add", ".")
	run("commit", "-qm", "main")

	if sign {
		run("config", "commit.gpgsign", "true")
	}

	run("merge", "--no-commit", "--no-ff", "side")

	// The setup asserts its own precondition. A helper that tolerates a merge
	// which did not happen hands every test below a repository with no
	// MERGE_HEAD, where the guard correctly says nothing and all three pass or
	// fail for reasons that have nothing to do with the guard. This repository
	// has shipped a test that passed for exactly that kind of wrong reason.
	check := exec.Command("git", "rev-parse", "-q", "--verify", "MERGE_HEAD")
	check.Dir = dir
	if err := check.Run(); err != nil {
		t.Fatalf("setup did not leave a merge in progress, so nothing below is testing the guard: %v", err)
	}
	return dir
}

func inDir(t *testing.T, dir string, f func() int) int {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	return f()
}

// The case that cost a pull request: an unsigned merge, which signedpush
// cannot replay and would refuse at push time instead.
func TestGuardMergeRefusesAnUnpublishableMerge(t *testing.T) {
	dir := repoMidMerge(t, false)
	if code := inDir(t, dir, func() int { return guardMerge(nil) }); code == 0 {
		t.Error("a merge commit that signedpush cannot publish was allowed through, so it would be refused at push time instead - after the tests, and at the cost of a rebase and a recreated ref")
	}
}

// The guard must not block the one party doing nothing wrong. The human signs
// locally and pushes with plain git, so a merge commit from them publishes
// fine - and the Update branch button on an epoch branch produces exactly one.
// The push guard was shipped once refusing precisely this party.
func TestGuardMergeAllowsAMergeThatWillBeSigned(t *testing.T) {
	dir := repoMidMerge(t, true)
	if code := inDir(t, dir, func() int { return guardMerge(nil) }); code != 0 {
		t.Error("a locally-signed merge was refused. That party publishes with plain git and needs no replay, so this guard would be an outage for the only people who can comply")
	}
}

// Ordinary commits are the overwhelming majority and must pay nothing.
func TestGuardMergeIsSilentWhenNoMergeIsHappening(t *testing.T) {
	dir := repoMidMerge(t, false)
	cmd := exec.Command("git", "merge", "--abort")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("merge --abort: %v", err)
	}
	if code := inDir(t, dir, func() int { return guardMerge(nil) }); code != 0 {
		t.Error("an ordinary commit was refused, so every commit in the repository now fails")
	}
}
