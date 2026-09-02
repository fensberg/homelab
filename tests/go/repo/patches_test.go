package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Reading a protected file as it is *going* to be.
//
// `.github/workflows/**` is behind the agent boundary: the App has no
// `workflows` permission, deliberately, so a workflow change is handed over as
// a patch in .github/patches and applied by a person. That leaves a window
// where the change is committed and reviewable but the file it edits is not
// yet edited.
//
// A test asserting a property of a workflow is red for the whole of that
// window, and the pre-push hook runs the tests - so the branch cannot be
// pushed by the only party that can write the test, to fix a file only the
// other party can write. That is a guard refusing the sole available route,
// which is not a guard, it is an outage. It happened on the first branch that
// carried both.
//
// So a workflow property is judged against the workflow plus every outstanding
// patch that touches it. Not an exemption: the patch is committed, it is in
// the diff being reviewed, and the property still has to hold - it just holds
// against the intended content rather than against a half-applied state. Once
// the patch is applied and removed, there is nothing to apply and the file
// alone has to satisfy it.
//
// This does not create a way to be green while broken. The workflow that
// actually runs is the file, so the lane it belongs to fails for real until
// somebody applies the patch. This only stops a second, redundant red check
// from blocking the hand-over.

// intendedWorkflow returns a workflow's content with every outstanding patch
// that touches it applied, using git itself rather than a diff parser.
func intendedWorkflow(t *testing.T, name string) string {
	t.Helper()
	root := repoRoot(t)
	rel := filepath.Join(".github", "workflows", name)

	body, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}

	patches := outstandingPatches(t)
	if len(patches) == 0 {
		return string(body)
	}

	// A scratch tree holding just this file at its repository path, so `git
	// apply` sees the paths the patch names.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(rel)), 0o755); err != nil {
		t.Fatalf("preparing a scratch tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, rel), body, 0o644); err != nil {
		t.Fatalf("preparing a scratch tree: %v", err)
	}

	for _, p := range patches {
		if !patchTouches(t, p, rel) {
			continue
		}
		cmd := exec.Command("git", "apply", p)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s is outstanding and does not apply to %s: %v\n%s\n\n"+
				"A patch that no longer applies is worse than no patch: it looks "+
				"like a hand-over that is still good, and it is not. Regenerate it "+
				"against the current file.",
				filepath.Base(p), rel, err, out)
		}
	}

	out, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatalf("reading the patched %s: %v", rel, err)
	}
	return string(out)
}

func outstandingPatches(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(repoRoot(t), ".github", "patches"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".patch") {
			out = append(out, filepath.Join(repoRoot(t), ".github", "patches", e.Name()))
		}
	}
	return out
}

func patchTouches(t *testing.T, patch, rel string) bool {
	t.Helper()
	body, err := os.ReadFile(patch)
	if err != nil {
		t.Fatalf("reading %s: %v", patch, err)
	}
	return strings.Contains(string(body), "b/"+rel)
}

// Every outstanding patch has to apply to the tree it is outstanding against.
//
// .github/patches/README.md asserts each one is verified against a clean tree
// before it is committed. Nothing checked that, and nothing rechecked it after
// the file underneath moved - so a patch could sit there looking like a
// hand-over that is still good, and fail in the hands of the one person who
// can apply it, who has no way to tell a stale patch from a live one.
func TestEveryOutstandingPatchStillApplies(t *testing.T) {
	root := repoRoot(t)
	patches := outstandingPatches(t)
	if len(patches) == 0 {
		t.Skip("no patches are outstanding")
	}

	for _, p := range patches {
		name := filepath.Base(p)
		cmd := exec.Command("git", "apply", "--check", p)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s does not apply to the current tree: %v\n%s\n\n"+
				"Regenerate it, or delete it if it has already been applied. A stale "+
				"patch is indistinguishable from a live one to whoever has to run it.",
				name, err, out)
		}
	}
}
