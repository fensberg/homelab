package main

import (
	"homelab/details/repopath"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The finding that started this, checked against the file it was about.
//
// #233: `.github/workflows/clerk.yml:261` - "Duplicate key 'ref' is specified
// inside the upload-sarif step." There are three `ref:` keys in that file, in
// three different steps' `with:` mappings, so none duplicates another. It was
// the clerk's first substantive finding and it was wrong.
func TestADuplicateKeyClaimIsCheckedAgainstTheFile(t *testing.T) {
	// The shape that produced it: the same key at the same indentation, three
	// times, in three separate mappings.
	body := `jobs:
  clerk:
    steps:
      - uses: actions/checkout@v5
        with:
          ref: ${{ github.event.pull_request.head.sha }}
      - uses: github/codeql-action/upload-sarif@v3
        with:
          sarif_file: snag.sarif
          ref: refs/pull/1/head
      - uses: github/codeql-action/upload-sarif@v3
        with:
          sarif_file: handover.sarif
          ref: refs/pull/1/head
`
	if dupes := duplicateKeys(body); len(dupes) != 0 {
		t.Fatalf(`three keys in three mappings were read as duplicates: %v

That is the model's mistake reproduced in code. Dropping a TRUE finding is the
one outcome worse than showing a false one, so this check must be exact.`, dupes)
	}

	s := snag{ruleUnsound, "clerk.yml", 6, "Duplicate key 'ref' is specified inside the upload-sarif step."}
	why, wrong := falsified(s, body)
	if !wrong {
		t.Fatal("the finding was not recognised as checkably false, so it would reach " +
			"the operator to be disproved by hand")
	}
	if !strings.Contains(why, "there is none") {
		t.Errorf("the discard reason does not say what was checked: %q", why)
	}
}

// A real duplicate is still reported.
//
// The check exists to remove false findings, not findings. If it dropped a true
// one the clerk would be quietly worse than before, and the failure would be
// invisible - a finding that never appears looks exactly like a file with
// nothing wrong in it.
func TestARealDuplicateKeyIsNotDiscarded(t *testing.T) {
	body := `jobs:
  clerk:
    runs-on: ubuntu-latest
    runs-on: self-hosted
`
	dupes := duplicateKeys(body)
	if len(dupes) != 1 || dupes[0] != "runs-on" {
		t.Fatalf("a genuine duplicate in one mapping was not found: %v", dupes)
	}

	s := snag{ruleUnsound, "clerk.yml", 4, "Duplicate key 'runs-on' in the clerk job."}
	if why, wrong := falsified(s, body); wrong {
		t.Errorf("a true finding was discarded as false: %s", why)
	}
}

// A claim naming a key that is not the duplicated one is still false.
func TestAClaimNamingTheWrongKeyIsDiscarded(t *testing.T) {
	body := `a:
  x: 1
  x: 2
  y: 3
`
	s := snag{ruleUnsound, "x.yml", 3, "Duplicate key 'y' here."}
	why, wrong := falsified(s, body)
	if !wrong {
		t.Fatal("a claim about a key that is not duplicated was kept")
	}
	if !strings.Contains(why, "x") {
		t.Errorf("the reason should name the keys that ARE duplicated: %q", why)
	}
}

// Anything this cannot settle exactly is left alone.
//
// "I could not check this" and "this is false" must never be the same answer.
// Only one of them is a reason to throw a finding away, and the cost of
// confusing them falls entirely on findings that were right.
func TestWhatCannotBeSettledIsLeftAlone(t *testing.T) {
	cases := []struct {
		name string
		s    snag
		body string
	}{
		{"not a duplicate-key claim", snag{ruleUnsound, "x.yml", 1, "nothing reaches this branch"}, "a: 1\n"},
		{"not a YAML file", snag{ruleUnsound, "x.go", 1, "duplicate key 'a'"}, "a: 1\na: 2\n"},
		{"file was not read", snag{ruleUnsound, "x.yml", 1, "duplicate key 'a'"}, ""},
		{"duplicate claimed, none named, one exists", snag{ruleUnsound, "x.yml", 1, "there is a duplicate key here"}, "m:\n  a: 1\n  a: 2\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if why, wrong := falsified(tc.s, tc.body); wrong {
				t.Errorf("discarded a finding this cannot settle: %s", why)
			}
		})
	}
}

// A sequence entry starts a fresh mapping.
//
// This is the exact distinction the model failed to make, so it is asserted
// directly rather than only through the case above.
func TestASequenceEntryStartsANewMapping(t *testing.T) {
	same := "list:\n  - name: a\n    value: 1\n  - name: b\n    value: 2\n"
	if dupes := duplicateKeys(same); len(dupes) != 0 {
		t.Errorf("keys in separate sequence entries were read as duplicates: %v", dupes)
	}
	within := "list:\n  - name: a\n    name: b\n"
	if dupes := duplicateKeys(within); len(dupes) != 1 {
		t.Errorf("a duplicate WITHIN one sequence entry was missed: %v", dupes)
	}
}

// A key inside a block scalar is data, not a key.
//
// Counting it would report a duplicate that is not there, which would discard a
// true finding - the failure this whole file is careful about.
func TestKeysInsideABlockScalarAreNotCounted(t *testing.T) {
	body := `entry:
  payload: |
    name: a
    name: a
  name: real
`
	if dupes := duplicateKeys(body); len(dupes) != 0 {
		t.Errorf("lines inside a block scalar were counted as keys: %v", dupes)
	}
}

// And the real workflows in this repository contain no duplicate keys.
//
// A check whose exactness matters is worth running over the actual corpus: if
// it reports a duplicate in a file that has none, it is about to start
// discarding true findings, and nothing else would say so.
func TestTheRepositorysOwnWorkflowsHaveNoDuplicateKeys(t *testing.T) {
	// Found, not counted, and a failure rather than a skip when it is missing.
	// It skipped: so the day this file moved and "../.." stopped reaching the
	// repository, the check would have reported nothing and passed.
	dir, err := repopath.Join(".github", "workflows")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !isYAML(e.Name()) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		checked++
		if dupes := duplicateKeys(string(body)); len(dupes) != 0 {
			t.Errorf("%s reported duplicate key(s) %v. Either the file really has "+
				"them, or this check is wrong and about to discard true findings.",
				e.Name(), dupes)
		}
	}
	const atLeastTenWorkflows = 10
	if checked < atLeastTenWorkflows {
		t.Fatalf("only %d workflow(s) were checked, so this proves nothing about the corpus", checked)
	}
}
