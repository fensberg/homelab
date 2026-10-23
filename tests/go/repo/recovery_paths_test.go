package repo

import (
	"path/filepath"
	"regexp"
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
	heavy(t, "semgrep reads every script, document and workflow for the path, a few seconds")

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

		// This file names the path in order to refuse it.
		filepath.Join("tests", "go", "repo", "recovery_paths_test.go"): true,
	}

	// What the repository tracks, and nothing else on disk: a build output,
	// a dependency and a site's pinned copy of the repository are all there
	// to be walked, and none of them is this repository's to answer for.
	var files []string
	for _, rel := range tracked(t, func(string) bool { return true }) {
		switch filepath.Ext(rel) {
		case ".go", ".md", ".sh", ".yml", ".yaml":
		default:
			continue
		}
		if !exempt[rel] && !isLedger(rel) {
			files = append(files, rel)
		}
	}
	checked := len(files)
	wrong := map[string]any{
		"id": "the-ci-binary", "languages": []string{"generic"}, "severity": "INFO",
		"message": "ci", "metadata": map[string]any{"about": "ci"},
		"pattern-regex": regexp.QuoteMeta("./scripts/contractor/" + "contractor"),
	}
	named := map[string]bool{}
	for _, f := range scanText(t, repository(t), []map[string]any{wrong}, files) {
		named[f.Path] = true
	}
	for _, rel := range files {
		if named[rel] {
			t.Errorf("%s tells a reader to run ./scripts/contractor/contractor, which only exists in CI.\n\n"+
				"`task build` puts the binary in toolshed/, so that is what a workstation has - and every "+
				"message a human reads is read on a workstation. Use ./toolshed/contractor.", rel)
		}
	}
	if checked == 0 {
		t.Fatal("no files checked, so this test proves nothing")
	}
}
