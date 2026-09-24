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
// This was the interim guard, and it is no longer interim: the workflows build
// to toolshed/ like everything else now, so there is ONE build output and the
// path it names is the only one there is (#215).
//
// The two workflow exemptions are gone with it, which is the point. Every
// exemption is a place the next person has to work out which of two correct
// paths applies, and a guard that says "use the other path here" was a note
// about the duplication rather than a cure for it.
func TestRecoveryInstructionsNameTheWorkstationBinary(t *testing.T) {
	root := repoRoot(t)

	exempt := map[string]bool{
		// .gitignore keeps the old build path while any branch predating the
		// toolshed move is still open - see the trigger written beside it and
		// TestNoCompiledBinaryIsUntrackedAndUnignored, which catches what that
		// window lets through.
		".gitignore": true,

		// The mutation ledger has to hold the broken form: its entry for this
		// very guard puts the wrong path back and requires this to go red. A
		// guard and the proof that it works cannot both refuse to contain the
		// thing being guarded against.
		filepath.Join("tests", "mutations.yml"): true,

		// This file names the path in order to refuse it.
		filepath.Join("tests", "go", "repo", "recovery_paths_test.go"): true,
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

		var text string
		if dir := filepath.Dir(rel); dir == filepath.Join(".github", "workflows") {
			text = workflowText(t, d.Name())
		} else {
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			text = string(body)
		}
		checked++
		if strings.Contains(text, "./scripts/contractor/"+"contractor") {
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
