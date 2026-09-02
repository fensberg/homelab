package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every Go program's binary must be gitignored.
//
// The taskfile builds each one next to its source rather than into a temporary
// directory, deliberately: `go run`'s wrapper process swallows SIGINT, which
// orphaned real infrastructure once. The cost of that choice is a build
// artifact sitting in the tree, and an artifact nobody ignored gets committed.
//
// It happened while adding scripts/supplierguard: `git add -A` swept up a
// three-megabyte binary, and because the guard is built by the commit hook on
// every commit, pre-commit then reported "files were modified by this hook" and
// refused - the guard blocking work by existing. The binary reached a pushed
// commit before anybody noticed, and non_fast_forward means a pushed commit
// cannot be repaired.
//
// So this checks the ignore rule exists for every program, rather than for the
// four that happened to be remembered.
func TestEveryGoProgramsBinaryIsIgnored(t *testing.T) {
	root := repoRoot(t)
	ignore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(root, "scripts"))
	if err != nil {
		t.Fatalf("reading scripts/: %v", err)
	}

	found := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, "scripts", e.Name())
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
			continue // not a Go program
		}
		found++

		// The taskfile builds `-o <name>` inside the program's own directory.
		want := "scripts/" + e.Name() + "/" + e.Name()
		if !strings.Contains(string(ignore), want) {
			t.Errorf(".gitignore does not ignore %s.\n\n"+
				"The taskfile builds it there on every invocation, so `git add -A` will "+
				"commit a binary - and where that binary is built by a commit hook, "+
				"pre-commit then refuses every commit with \"files were modified by this "+
				"hook\", which is the guard blocking work by existing.", want)
		}
	}

	if found == 0 {
		t.Fatal("no Go programs found under scripts/, so this check guards nothing - and " +
			"passing on an empty set is how a check stops mattering")
	}
}

// And nothing already committed is one of those binaries. The ignore rule
// stops the next one; this catches one that got in before the rule existed.
func TestNoBuiltBinaryIsTracked(t *testing.T) {
	root := repoRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "scripts"))
	if err != nil {
		t.Fatalf("reading scripts/: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		binary := filepath.Join(root, "scripts", e.Name(), e.Name())
		info, err := os.Stat(binary)
		if err != nil || info.IsDir() {
			continue
		}
		// Present on disk is fine and expected - it is built. Tracked is not.
		if trackedInGit(t, root, "scripts/"+e.Name()+"/"+e.Name()) {
			t.Errorf("scripts/%s/%s is tracked in git. It is a build artifact: it changes "+
				"on every build, bloats the history, and cannot be removed from a pushed "+
				"commit because non_fast_forward applies to every branch here.",
				e.Name(), e.Name())
		}
	}
}

func trackedInGit(t *testing.T, root, path string) bool {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", path)
	return cmd.Run() == nil
}
