package repo

import (
	"path/filepath"
	"strings"
	"testing"

	"homelab/tests/parsed"
)

// The detectors are proved where they are, in tests/parsed; this runs them
// over the repository itself.

// --- the repository is clean ------------------------------------------------

func TestRepositoryHasNoDuplicateKeys(t *testing.T) {
	t.Parallel()
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
