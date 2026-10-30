package parsed

import (
	"strings"
	"testing"
)

// The detectors detect. A check that only ever asserts "the repository is
// clean" is indistinguishable from one that is silently broken, and would
// stay green forever after someone breaks it; tests/go/repo runs them over
// the repository itself.

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
	got := envDuplicates(content)
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
	if got := envDuplicates(content); len(got) != 0 {
		t.Errorf("got %v, want none - comments must not count as assignments", got)
	}
}

func TestJSONDuplicates(t *testing.T) {
	content := `{
	  "baselines": { "go": 24.5, "js": 0, "go": 30.0 },
	  "other": 1
	}`
	got, err := jsonDuplicates(content)
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
	got, err := jsonDuplicates(content)
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
    runs-on: alpha-runners
`
	got, err := yamlDuplicates(content)
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
	got, err := yamlDuplicates(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want none - separate documents are not one mapping", got)
	}
}
