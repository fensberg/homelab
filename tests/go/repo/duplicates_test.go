package repo

import (
	"homelab/details/repopath"
	"homelab/tests/parsed"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The detectors are proved where they are, in tests/parsed; this runs them
// over the repository itself.

// --- the repository is clean ------------------------------------------------

// repoRootEnv redirects every test in this package at a different copy of the
// repository.
//
// It exists for the mutation ledger, which proves each guard fails when the
// thing it guards is broken. Proving that means running a guard against a
// deliberately broken repository, and breaking the real working tree to do it
// is not acceptable - a crashed run would leave the developer's checkout
// mutated. So the ledger copies the tracked files into a scratch directory,
// breaks the copy, and points the test binary at it with this.
//
// Nothing else sets it, and it is not an escape hatch: pointing a test run at
// a different tree does not make any assertion weaker, it just makes it about
// a different tree.
const repoRootEnv = "HOMELAB_REPO_ROOT"

func repoRoot(t *testing.T) string {
	t.Helper()
	if override := os.Getenv(repoRootEnv); override != "" {
		if _, err := os.Stat(filepath.Join(override, "CLAUDE.md")); err != nil {
			t.Fatalf("%s is set to %s, which does not look like the repository: %v",
				repoRootEnv, override, err)
		}
		return override
	}
	// The override above is what the mutation ledger depends on and it stays
	// here: the ledger breaks a scratch copy of the tree and points these guards
	// at it. repopath.Root walks from its own source file, so it always answers
	// with the real tree - fine for everything else, and exactly wrong for a
	// guard being proved against a mutation.
	root, err := repopath.Root()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRepositoryHasNoDuplicateKeys(t *testing.T) {
	tree := repository(t)
	checked := 0

	for _, rel := range trackedMatching(t, func(rel string) bool { return authoredHere(t, rel) }) {
		dupes, ok := tree.DuplicateKeys(t, rel)
		if ok {
			checked++
		}
		for _, d := range dupes {
			t.Errorf(`%s declares %q more than once (%d times) in %s.

Every parser this project uses keeps the last one and reports nothing, so
this would not have failed anything else. If both copies are wanted, they
are not - one of them is dead.`, rel, d.Key, d.Count, orTopLevel(d.Path))
		}
	}

	// A walk that silently matched nothing would pass forever.
	if checked < 10 {
		t.Fatalf("only %d files were checked; the enumeration or the extension "+
			"list is wrong", checked)
	}
	t.Logf("checked %d config files for duplicate keys", checked)
}

func orTopLevel(path string) string {
	if path == "" {
		return "the top level"
	}
	return path
}

// Where the guards are, and the one package they read the repository
// through.
var (
	guardsDir = filepath.Join("tests", "go", "repo")
	readerDir = filepath.Join("tests", "go", "parsed")
)

// repository is the tree this package's guards judge, read through
// tests/parsed and only through it.
func repository(t *testing.T) *parsed.Tree {
	t.Helper()
	return parsed.Repository(t)
}

// mentionedIn reports whether any piece of a Markdown document - its prose,
// a code span, a code block - says the phrase.
func mentionedIn(t *testing.T, rel, phrase string) bool {
	t.Helper()
	for _, part := range repository(t).Markdown(t, rel) {
		if strings.Contains(part.Text, phrase) {
			return true
		}
	}
	return false
}
