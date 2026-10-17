package repo

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
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
