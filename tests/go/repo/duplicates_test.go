package repo

import (
	"os"
	"path/filepath"
	"runtime"
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
	if override := os.Getenv(repoRootEnv); override != "" {
		if _, err := os.Stat(filepath.Join(override, "CLAUDE.md")); err != nil {
			t.Fatalf("%s is set to %s, which does not look like the repository: %v",
				repoRootEnv, override, err)
		}
		return override
	}
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine this source file's location")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Fatalf("computed repo root %s does not look like the repository: %v", root, err)
	}
	return root
}

// Directories that hold files this repository did not write and does not
// edit. A finding inside any of them is a finding about somebody else's
// generated output, which nobody here can act on.
var skipDirs = map[string]bool{
	".git":         true,
	".terraform":   true,
	"node_modules": true,
	"coverage":     true,
	// Flux's own generated install manifest, committed verbatim - the same
	// exclusion .checkov.yaml and super-linter.vars already make.
	"flux-system": true,
}

// Generated integrity databases and machine-written state. Same reasoning.
var skipFiles = map[string]bool{
	"pnpm-lock.yaml": true,
	"go.sum":         true,
}

func TestRepositoryHasNoDuplicateKeys(t *testing.T) {
	root := repoRoot(t)
	checked := 0

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if skipFiles[d.Name()] {
			return nil
		}

		dupes, checkErr := Check(path)
		if checkErr != nil {
			// A parse failure is not this test's business to report - the
			// format's own validator owns that, and saying it twice would
			// put two owners on one check.
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if strings.HasSuffix(path, ".json") || strings.HasSuffix(path, ".yaml") ||
			strings.HasSuffix(path, ".yml") || strings.HasSuffix(path, ".vars") ||
			strings.HasSuffix(path, ".env") {
			checked++
		}
		for _, d := range dupes {
			t.Errorf(`%s declares %q more than once (%d times) in %s.

Every parser this project uses keeps the last one and reports nothing, so
this would not have failed anything else. If both copies are wanted, they
are not - one of them is dead.`, rel, d.Key, d.Count, orTopLevel(d.Path))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}

	// A walk that silently matched nothing would pass forever.
	if checked < 10 {
		t.Fatalf("only %d files were checked; the walk or the extension list is wrong", checked)
	}
	t.Logf("checked %d config files for duplicate keys", checked)
}

func orTopLevel(path string) string {
	if path == "" {
		return "the top level"
	}
	return path
}
