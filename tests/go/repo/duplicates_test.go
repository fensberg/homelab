package repo

import (
	"fmt"
	"homelab/details/repopath"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Two halves. The first proves the detectors actually detect - a check that
// only ever asserts "the repository is clean" is indistinguishable from a
// check that is silently broken, and would stay green forever after someone
// breaks it. The second runs them over the repository itself.

// --- the detectors detect ---------------------------------------------------

func TestEnvDuplicates(t *testing.T) {
	// The exact shape of the defect that prompted this package: a merge kept
	// both sides' copy of the same assignment.
	content := `
# Super-Linter settings
VALIDATE_GO=false

# This root is plain OpenTofu, never Terragrunt.
VALIDATE_TERRAGRUNT=false

# This root is plain OpenTofu, never Terragrunt.
VALIDATE_TERRAGRUNT=false

VALIDATE_JSCPD=false
`
	got := EnvDuplicates(content)
	if len(got) != 1 {
		t.Fatalf("got %d duplicates, want 1: %v", len(got), got)
	}
	if got[0].Key != "VALIDATE_TERRAGRUNT" || got[0].Count != 2 {
		t.Errorf("got %+v, want VALIDATE_TERRAGRUNT x2", got[0])
	}
}

func TestEnvDuplicates_IgnoresCommentsAndProse(t *testing.T) {
	// A commented-out assignment is not an assignment, and these files carry
	// long explanatory comments that must not be parsed as config.
	content := `
# VALIDATE_GO=false
# VALIDATE_GO=false
VALIDATE_GO=false
Some prose that mentions VALIDATE_GO=false in passing is not a line here.
`
	if got := EnvDuplicates(content); len(got) != 0 {
		t.Errorf("got %v, want none - comments must not count as assignments", got)
	}
}

func TestJSONDuplicates(t *testing.T) {
	content := `{
	  "baselines": { "go": 24.5, "js": 0, "go": 30.0 },
	  "other": 1
	}`
	got, err := JSONDuplicates(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d duplicates, want 1: %v", len(got), got)
	}
	if got[0].Key != "go" || got[0].Path != "baselines" {
		t.Errorf("got %+v, want key \"go\" under \"baselines\"", got[0])
	}
}

func TestJSONDuplicates_NestedAndArrays(t *testing.T) {
	// A key repeated inside an array element must still be found, and the
	// same key name at two different levels must not be.
	content := `{
	  "name": "outer",
	  "cases": [
	    { "name": "a", "name": "b" },
	    { "name": "c" }
	  ]
	}`
	got, err := JSONDuplicates(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d duplicates, want 1: %v", len(got), got)
	}
	if !strings.Contains(got[0].Path, "cases[0]") {
		t.Errorf("got path %q, want the duplicate located in cases[0]", got[0].Path)
	}
}

func TestYAMLDuplicates(t *testing.T) {
	content := `
jobs:
  test:
    runs-on: ubuntu-latest
    runs-on: ` + scaleSetName() + `
`
	got, err := YAMLDuplicates(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Key != "runs-on" {
		t.Fatalf("got %v, want one duplicate of runs-on", got)
	}
}

// Kubernetes manifests legitimately hold several documents in one file. The
// same key in two different documents is not a duplicate.
func TestYAMLDuplicates_MultiDocumentIsNotADuplicate(t *testing.T) {
	content := `
apiVersion: v1
kind: Namespace
---
apiVersion: v1
kind: Secret
`
	got, err := YAMLDuplicates(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want none - separate documents are not one mapping", got)
	}
}

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
	root, err := resolveRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// resolveRepoRoot is repoRoot for code that has no test to fail, and returns
// the error instead.
func resolveRepoRoot() (string, error) {
	if override := os.Getenv(repoRootEnv); override != "" {
		if _, err := os.Stat(filepath.Join(override, "CLAUDE.md")); err != nil {
			return "", fmt.Errorf("%s is set to %s, which does not look like the repository: %w",
				repoRootEnv, override, err)
		}
		return override, nil
	}
	// The override above is what the mutation ledger depends on and it stays
	// here: the ledger breaks a scratch copy of the tree and points these guards
	// at it. repopath.Root walks from its own source file, so it always answers
	// with the real tree - fine for everything else, and exactly wrong for a
	// guard being proved against a mutation.
	return repopath.Root()
}

func TestRepositoryHasNoDuplicateKeys(t *testing.T) {
	root := repoRoot(t)
	checked := 0

	for _, rel := range trackedMatching(t, authoredHere) {
		dupes, checkErr := Check(filepath.Join(root, rel))
		if checkErr != nil {
			// A parse failure is not this test's business to report - the
			// format's own validator owns that, and saying it twice would
			// put two owners on one check.
			continue
		}
		if strings.HasSuffix(rel, ".json") || strings.HasSuffix(rel, ".yaml") ||
			strings.HasSuffix(rel, ".yml") || strings.HasSuffix(rel, ".vars") ||
			strings.HasSuffix(rel, ".env") {
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
