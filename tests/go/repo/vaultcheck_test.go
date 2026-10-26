package repo

import (
	"go/ast"
	"go/token"
	"testing"
)

// `contractor check-inventory` must never be able to disclose a secret.
//
// The whole reason it exists is to be safe to run and safe to share: it
// answers "does the vault still match the config template" and its output is
// meant to be pasteable into an issue or a pull request. That property is not
// a promise the code makes in a comment - it has to survive somebody adding a
// debug line, widening a return value, or "improving" the report to say what
// it actually found.
//
// It also cannot be enforced by the type system alone, because of a
// constraint this project already documented elsewhere: `op` has no existence
// check that does not also return the value. Probing a reference necessarily
// reads the secret. The design answer is to read it into a local, measure it,
// and drop it - onepassword.Probe returns a Status and nothing else, so a
// caller cannot print what it was never handed.
//
// The code rules in policy/semgrep/vault-check.yml are what stop that design
// being quietly undone: Probe's one return value, and no call in the check
// that fetches a value, stores one or writes a file.

// Guard against those rules passing because the feature was renamed or
// removed: renaming the *call* while keeping the files would not be caught by
// anything else.
func TestVaultCheckStillExists(t *testing.T) {
	light(t)
	probe := repository(t).Go(t, token.NewFileSet(), "scripts/contractor/internal/phases/checkvault.go")
	// The report has to actually be driven by a probe, or the rules in
	// policy/semgrep/vault-check.yml are guarding a function nobody calls.
	calls := false
	ast.Inspect(probe, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Probe" {
			return true
		}
		if id, ok := ast.Unparen(sel.X).(*ast.Ident); ok && id.Name == "onepassword" {
			calls = true
		}
		return true
	})
	if !calls {
		t.Fatal(`checkvault.go no longer calls onepassword.Probe.

Either the check now resolves references some other way - in which case the
rules in policy/semgrep/vault-check.yml need to point at that instead, and the
new way needs the same guarantee - or the check is gone and those rules should
go with it. Passing quietly is the
one outcome that is wrong.`)
	}
}
