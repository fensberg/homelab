package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The contractor does not shell out to gh.
//
// It used to, for one question - are there queued deploy runs for this site -
// and the first integration-tier run in this repository's history halted on
// `exec: "gh": executable file not found in $PATH`. The runner image does not
// carry gh, the hand-written binary list in the image guard did not name it,
// and installing it would not have helped: that job holds contents:read and
// listing runs needs actions:read.
//
// So the dependency came out. This keeps it out: gh is the kind of tool that
// is present on every developer's machine and absent from every image, which
// is exactly the shape that passes review and fails in CI.
func TestTheContractorDoesNotShellOutToGh(t *testing.T) {
	root := filepath.Join(repoRoot(t), "scripts", "contractor")

	var checked int
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		checked++
		// The invocation, not the word: `gh auth login` in an error message is
		// prose, and prose is not a dependency.
		if strings.Contains(string(body), `"gh",`) || strings.Contains(string(body), `"gh")`) {
			rel, _ := filepath.Rel(repoRoot(t), path)
			t.Errorf("%s invokes gh. It is absent from the runner image, and the job that would "+
				"need it holds no actions:read - ask the GitHub API directly instead.", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking scripts/contractor: %v", err)
	}
	if checked == 0 {
		t.Fatal("no Go sources checked, so this test proves nothing")
	}
}
