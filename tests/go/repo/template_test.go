package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every op:// reference in the template must be wrapped in {{ }}.
//
// `op inject` substitutes only the moustache form. A bare op:// reference is
// still valid JSON, still passes every schema check, and is copied through to
// the rendered config verbatim - so the program downstream receives the
// literal string "op://homelab/state_backup/recipient" where it expected an
// age recipient, a hostname or a password.
//
// The Render phase does catch this (AssertRenderedConfigComplete compares the
// template against what came back), but only during a real run, against a real
// vault. This is the same check one tier earlier and with no credentials, so a
// pull request catches it instead of an ignition at 2am. It cost a real bug:
// the estate's backup recipient was added unwrapped when the keypair moved out
// of the per-site item.
func TestEveryVaultReferenceInTheTemplateIsWrapped(t *testing.T) {
	path := filepath.Join(repoRoot(t), "config", "management.tpl.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the config template: %v", err)
	}

	wrapped := regexp.MustCompile(`\{\{\s*op://[^}]*\}\}`)
	stripped := wrapped.ReplaceAllString(string(body), "")

	var bare []string
	for i, line := range strings.Split(stripped, "\n") {
		if strings.Contains(line, "op://") {
			bare = append(bare, fmt.Sprintf("  line %d: %s", i+1, strings.TrimSpace(line)))
		}
	}
	if len(bare) > 0 {
		t.Errorf(`config/management.tpl.json has %d unwrapped op:// reference(s):

%s

op inject only substitutes {{ op://... }}. Unwrapped, the literal reference
string is handed to whatever reads that key.`, len(bare), strings.Join(bare, "\n"))
	}

	// Guard against the whole check passing because the file moved or emptied.
	if n := len(wrapped.FindAllString(string(body), -1)); n < 10 {
		t.Fatalf("only %d wrapped references found; this test is reading the wrong file", n)
	}
}
