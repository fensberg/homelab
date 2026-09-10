package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A phase that gives up must say what went wrong, not only what to do about it.
//
// The teardown discarded the error from `tofu destroy` and printed only advice
// - "check Proxmox manually" - so a real demolish failed on five VMs and told
// the operator nothing about why. The diagnostics tofu produced were collected
// by the summariser and thrown away one line above where they were needed.
//
// Advice without a cause is the shape to catch: it reads as helpful, passes
// review, and leaves the person holding it no better off than silence. This
// looks for the mechanical version of that mistake - a handler that binds an
// error and then never mentions it.
func TestNoPhaseSwallowsAnErrorItHandles(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "scripts", "contractor", "internal", "phases")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		checked++
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		body := string(raw)
		for _, at := range errHandler.FindAllStringIndex(body, -1) {
			block := body[at[0]:min(at[0]+900, len(body))]
			if end := strings.Index(block, "\n\t}"); end > 0 {
				block = block[:end]
			}
			// Only handlers that deal with the failure in place. One that
			// returns is covered by whoever it returns to.
			if !strings.Contains(block, "run.Warn") {
				continue
			}
			// A handler that returns the error onward has passed it to
			// somebody who will report it, which is the other correct answer.
			if strings.Contains(block, "err.Error()") ||
				strings.Contains(block, "%w") ||
				strings.Contains(block, "return err") ||
				strings.Contains(block, "return fmt.Errorf") {
				continue
			}
			t.Errorf("%s handles an error with run.Warn and never reports it:\n\n  %s\n\n"+
				"Advice with no cause reads as helpful and leaves the reader no better "+
				"off than silence. Print err, wrap it, or return it.",
				e.Name(), strings.TrimSpace(firstLine(block)))
		}
	}
	// The phases directory is this guard's whole subject, and reading zero
	// files from it passes: the loop below asserts something about each file,
	// so no files means no assertions and a green check. That is reachable by
	// ordinary maintenance - the phases moving one level down would have every
	// entry skipped as a directory - and it announces itself nowhere.
	//
	// A real minimum rather than "at least one", because the interesting
	// failure is a filter that still matches something. There were 24 at the
	// time of writing; this is deliberately well below that, so it catches a
	// collapse without failing on ordinary consolidation.
	if checked < 10 {
		t.Fatalf("only %d phase source files were checked, which cannot be right - this guard is reading the wrong directory and proves nothing", checked)
	}
}

// `if err := ...; err != nil {`, whatever follows it.
//
// The first version of this required run.Warn to be the very next line, which
// meant writing a comment above the Warn exempted the code from the check
// entirely - and the fix this test exists to protect had exactly such a
// comment. A guard that a comment can switch off is not a guard. The block is
// searched instead.
var errHandler = regexp.MustCompile(`if err := [^\n]*; err != nil \{`)
