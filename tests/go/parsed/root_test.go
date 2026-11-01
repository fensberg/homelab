package parsed

import (
	"path/filepath"
	"strings"
	"testing"

	"homelab/details/repopath"
)

// The directory the repository is read from is the top of the git checkout.
//
// Every guard reads the repository through this package, and this package
// lists it with git ls-files from repopath's answer - so an answer one level
// too deep does not break a guard, it shrinks it. Each would examine a
// subtree, find nothing wrong in it, and report green. repopath's walk stops
// at the first directory holding all of its markers; this checks that
// directory is the repository's top rather than trusting that no
// subdirectory will ever acquire them.
//
// Checked against git rather than against a path written here, so it holds in
// a worktree, a fork, or a checkout anywhere on disk. It calls repopath from
// the compiled binary, so no mutation of a scratch tree reaches the answer it
// checks: proved by hand, with -count=1, by editing repopath's walk to answer
// one directory too deep. The walk itself is unit-tested in
// scripts/details/repopath: TestANestedClaudeMdDoesNotBecomeTheRoot.
func TestTheRepositoryIsReadFromTheTopOfTheCheckout(t *testing.T) {
	root, err := repopath.Root()
	if err != nil {
		t.Fatal(err)
	}
	tree := &Tree{root: root}
	out, errs, err := tree.Invoke(Run{}, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatalf("asking git where the top of the repository is: %v\n%s", err, errs)
	}
	top := strings.TrimSpace(string(out))
	// Resolve symlinks on both sides: a temporary directory on some systems is
	// reached through one, and the comparison is about the place, not the name.
	rootReal, _ := filepath.EvalSymlinks(root)
	topReal, _ := filepath.EvalSymlinks(top)
	if rootReal != topReal {
		t.Errorf("repopath.Root() is %s, but the repository's top is %s.\n\n"+
			"Every guard reads the repository from repopath's answer, so each is now "+
			"examining a subtree and reporting green over the rest. Something below the "+
			"top has acquired every one of repopath.Markers - remove it, or make the "+
			"markers unambiguous again.", root, top)
	}
}

// A program a guard runs is a fixture, and its reports are not the run's: a
// retry's deliberate failures reached the real job summary as failed attempts.
func TestARunProgramIsNotGivenTheRunsSummary(t *testing.T) {
	t.Setenv("GITHUB_STEP_SUMMARY", "/the/run/summary")
	tree := &Tree{root: t.TempDir()}
	inherited, _, err := tree.Invoke(Run{}, "env")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(inherited), "GITHUB_STEP_SUMMARY=") {
		t.Error("a program was handed the run's job summary")
	}
	named, _, err := tree.Invoke(Run{Env: []string{"GITHUB_STEP_SUMMARY=/a/test/says"}}, "env")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(named), "GITHUB_STEP_SUMMARY=/a/test/says") {
		t.Error("a test could not name a summary of its own")
	}
}
