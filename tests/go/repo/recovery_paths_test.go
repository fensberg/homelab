package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A recovery instruction must name a binary the reader actually has.
//
// The Backup phase prints, at the end of every ignition, the command to bring
// the estate back after a total loss. On 2026-09-04 it printed
// `./scripts/contractor/contractor restore -site site0` - a path that does not
// exist on a workstation, because `task build` puts the binary in toolshed/.
//
// The reason is that there are two build outputs for one program. The
// workflows build `-o contractor` inside scripts/contractor and are correct to
// use that path; everything a human reads is correct only with toolshed/. The
// program was printing CI's path to somebody standing at a workstation, in the
// message they would be reading precisely because everything else was gone.
//
// This is the interim guard. The real fix is one build output, filed
// separately - a guard that says "use the other path here" is a note about the
// duplication rather than a cure for it.
func TestRecoveryInstructionsNameTheWorkstationBinary(t *testing.T) {
	root := repoRoot(t)

	// Workflows build to scripts/contractor/contractor themselves, so the path
	// is right there and only there. .gitignore names it for the same reason.
	exempt := map[string]bool{
		".github/workflows/deploy-infrastructure.yml": true,
		".github/workflows/integration-tests.yml":     true,
		".gitignore": true,

		// The mutation ledger has to hold the broken form: its entry for this
		// very guard puts the wrong path back and requires this to go red. A
		// guard and the proof that it works cannot both refuse to contain the
		// thing being guarded against.
		filepath.Join("tests", "mutations.yml"): true,
	}

	var checked int
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "toolshed", ".terraform":
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".go", ".md", ".sh", ".yml", ".yaml":
		default:
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil || exempt[rel] || rel == filepath.Join("tests", "go", "repo", "recovery_paths_test.go") {
			return nil
		}

		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		checked++
		if strings.Contains(string(body), "./scripts/contractor/"+"contractor") {
			t.Errorf("%s tells a reader to run ./scripts/contractor/contractor, which only exists in CI.\n\n"+
				"`task build` puts the binary in toolshed/, so that is what a workstation has - and every "+
				"message a human reads is read on a workstation. Use ./toolshed/contractor.", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
	if checked == 0 {
		t.Fatal("no files checked, so this test proves nothing")
	}
}
