package repo

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"homelab/details/repopath"
)

// The directory repopath answers with is the top of the git checkout.
//
// Every guard in this package enumerates the repository from that answer - git
// ls-files is run there - so an answer one level too deep does not break a
// guard, it shrinks it. Each would examine a subtree, find nothing wrong in it,
// and report green. The walk stops at the first directory holding all of
// repopath's markers; this checks that directory is the repository's top
// rather than trusting that no subdirectory will ever acquire them.
//
// Checked against git rather than against a path written here, so it holds in
// a worktree, a fork, or a checkout anywhere on disk.
func TestTheRootGuardsEnumerateFromIsTheWholeRepository(t *testing.T) {
	root, err := repopath.Root()
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", root, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("asking git where the top of %s is: %v", root, err)
	}
	top := strings.TrimSpace(string(out))
	// Resolve symlinks on both sides: a temporary directory on some systems is
	// reached through one, and the comparison is about the place, not the name.
	rootReal, _ := filepath.EvalSymlinks(root)
	topReal, _ := filepath.EvalSymlinks(top)
	if rootReal != topReal {
		t.Errorf("repopath.Root() is %s, but the repository's top is %s.\n\n"+
			"Every guard enumerates from repopath's answer, so each is now examining a "+
			"subtree and reporting green over the rest. Something below the top has "+
			"acquired every one of repopath.Markers - remove it, or make the markers "+
			"unambiguous again.", root, top)
	}
}
