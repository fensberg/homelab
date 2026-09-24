package repo

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Reading the tree as it is.
//
// `.github/workflows/**` is behind the agent boundary: the App has no
// `workflows` permission, deliberately, so a workflow change is handed over as
// a patch in .github/patches and applied by a person.
//
// These helpers used to read workflows as they would be once every
// outstanding patch was applied, so a branch carrying a patch stayed green
// while it waited. But they applied the patch to the workflows and to nothing
// else, and the tests doing the reading were the old tests. So a patch that
// changed a workflow and the test of it together produced a tree that was
// neither old nor new, and the old test failed against the new workflow.
//
// Everything that changes along with a workflow now travels inside its patch:
// the tests, the suppliers list, the ledger, the docs. The branch is
// consistently the old tree until the patch is applied and consistently the
// new tree afterwards, so the tree can simply be read as it is.

// workflowText returns a workflow's content, or "" if there is no such
// workflow.
func workflowText(t *testing.T, name string) string {
	t.Helper()
	rel := filepath.Join(".github", "workflows", name)
	body, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(body)
}

// workflowTexts returns every workflow at the top of .github/workflows, keyed
// by file name.
func workflowTexts(t *testing.T) map[string]string {
	t.Helper()
	dir := filepath.Join(repoRoot(t), ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("listing the workflows: %v", err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		out[e.Name()] = string(body)
	}
	return out
}

// repoFileText returns one file's content.
func repoFileText(t *testing.T, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(body)
}

// No test reads .github/patches.
//
// A patch exists for the seconds between the agent handing it over and the
// operator applying it, and everything that changes with it travels inside
// it. So there is nothing about the pending state worth testing, and tests
// that tried - reading workflows through pending patches, applying them to
// the ledger's scratch tree, checking a patch still applies - cost hours and
// guarded a window nobody runs in. The operator's words: "A test whose only
// purpose is to test a patch that won't exist in 5 seconds is a stupid test."
//
// This refuses the first step back towards that machinery.
func TestNoTestReadsThePatchesDirectory(t *testing.T) {
	root := repoRoot(t)
	self := filepath.Join("tests", "go", "repo", "patches_test.go")
	checked := 0
	for _, rel := range trackedMatching(t, func(p string) bool {
		return strings.HasPrefix(p, "tests/") && strings.HasSuffix(p, "_test.go")
	}) {
		if rel == self {
			continue
		}
		checked++
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		// Code only. A comment explaining why patches are absent is not a
		// test reading them.
		var code []string
		for _, line := range strings.Split(string(body), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "//") {
				code = append(code, line)
			}
		}
		src := strings.Join(code, "\n")
		if strings.Contains(src, `".github", "patches"`) || strings.Contains(src, ".github/patches") {
			t.Errorf("%s reads .github/patches.\n\n"+
				"A patch lives for the seconds between hand-over and apply, and carries "+
				"every change that goes with it. Test the tree as it is; there is no "+
				"pending state worth a test.", rel)
		}
	}
	if checked == 0 {
		t.Fatal("found no test files under tests/, so nothing was checked")
	}
}
