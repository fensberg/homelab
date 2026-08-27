package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The age identity is the break-glass: its entire job is to be the one thing
// that survives everything else being compromised. Two ways to destroy that
// property, both of which look like ordinary convenience at the time:
//
//  1. Referencing it from OpenTofu. Anything Terraform reads becomes a value
//     Terraform can persist, and state is exactly what the key exists to make
//     worthless. A leaked state file containing the key that decrypts the
//     backups of that same state is a closed loop.
//  2. Putting it in the rendered config, which lands on disk beside every
//     other secret and is read by the same program the key is meant to
//     outlive.
//
// Neither is caught by anything else - both would work perfectly, and the
// failure only shows up as an absence of protection nobody notices.

// The identity is addressed two ways, and both are forbidden: as the vault
// reference a human pastes, and as the config path OpenTofu would traverse if
// somebody added it to the template. "identity" on its own is too common a
// word to grep for without false positives, so the tokens are anchored to the
// item that holds it.
//
// "backup_identity" is the name this field had before the keypair moved out of
// the per-site item and up to the estate. It stays on the list so that
// reintroducing the old name does not quietly reintroduce the old exposure.
var breakGlassTokens = []string{
	"state_backup/identity",
	"state_backup.identity",
	"backup_identity",
}

func TestBreakGlassIdentityIsNeverReferencedByOpenTofu(t *testing.T) {
	root := repoRoot(t)
	checked := 0

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".tf") && !strings.HasSuffix(path, ".tftest.hcl") {
			return nil
		}
		checked++
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if tok, found := firstMatch(string(body), breakGlassTokens); found {
			rel, _ := filepath.Rel(root, path)
			t.Errorf(`%s references %q.

The break-glass identity must never be an OpenTofu value. Anything Terraform
reads is a value Terraform can write to state - and state is precisely what
this key exists to make worthless. A state file containing the key that
decrypts the backups of that state protects nothing.

It is read by scripts/ignite in Go, through op, and only during a restore.`, rel, tok)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
	if checked < 5 {
		t.Fatalf("only %d OpenTofu files were checked; the walk is wrong", checked)
	}
}

// The rendered config is written to disk by the Render phase and read by every
// later phase. The recipient belongs there - it is public and the Backup phase
// needs it. The identity does not.
func TestBreakGlassIdentityIsNotInTheConfigTemplate(t *testing.T) {
	path := filepath.Join(repoRoot(t), "config", "management.tpl.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the config template: %v", err)
	}
	if tok, found := firstMatch(string(body), breakGlassTokens); found {
		t.Errorf(`config/management.tpl.json references %q.

Adding it there would render the private half into
config/management.rendered.json on every run, next to every other secret. The
recipient belongs in the template because encryption needs it; the identity is
only ever needed by a human performing a restore.`, tok)
	}
	// Guard against the assertion silently passing because the file moved or
	// the block was renamed again.
	if !strings.Contains(string(body), "state_backup/recipient") {
		t.Fatal("the template no longer references state_backup/recipient - this test is checking the wrong file")
	}
}

func firstMatch(body string, tokens []string) (string, bool) {
	for _, tok := range tokens {
		if strings.Contains(body, tok) {
			return tok, true
		}
	}
	return "", false
}

// Where the identity may be read from.
//
// The two rules above keep it out of OpenTofu and out of the rendered config.
// This one keeps it out of the ignition sequence: the identity exists to be
// fetched when a human is putting the estate back together, and every other
// phase works with the recipient, which is public.
//
//   - restore.go is the one place that fetches the value and uses it, which is
//     what `-restore` is.
//   - secrets.go probes whether it is present, so a run can warn that backups
//     are being written to a key whose private half nobody has stored. `op`
//     has no existence check that does not also return the value, so this
//     honestly does read it - and immediately discards it. Worth naming rather
//     than pretending otherwise.
//
// Anything else reading it is a phase that has no business holding the key its
// own backups are protected from.
var mayReadBreakGlassIdentity = map[string]bool{
	"restore.go": true,
	"secrets.go": true,
}

func TestBreakGlassIdentityIsReadOnlyByTheRestorePath(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "scripts", "ignite")
	checked := 0

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		checked++
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		// The fetch, not the mention. Naming the reference in an error message
		// that tells an operator where to put the key is the whole point of
		// having a constant for it.
		if !strings.Contains(string(body), "onepassword.Read(") {
			return nil
		}
		if !strings.Contains(string(body), "IdentityRef") {
			return nil
		}
		if mayReadBreakGlassIdentity[filepath.Base(path)] {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		t.Errorf(`%s reads the break-glass identity.

Only the restore path may. Every phase in the ignition sequence works with the
recipient, which is public - a phase that holds the private half is a phase
that could decrypt the backups it is writing, which is the property this key
exists to deny.`, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walking scripts/ignite: %v", err)
	}
	if checked < 10 {
		t.Fatalf("only %d Go files were checked; the walk is wrong", checked)
	}
}
