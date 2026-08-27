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

const breakGlassField = "backup_identity"

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
		if strings.Contains(string(body), breakGlassField) {
			rel, _ := filepath.Rel(root, path)
			t.Errorf(`%s references %q.

The break-glass identity must never be an OpenTofu value. Anything Terraform
reads is a value Terraform can write to state - and state is precisely what
this key exists to make worthless. A state file containing the key that
decrypts the backups of that state protects nothing.

It is read by scripts/ignite in Go, through op, and only during a restore.`, rel, breakGlassField)
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
	if strings.Contains(string(body), breakGlassField) {
		t.Errorf(`config/management.tpl.json references %q.

Adding it there would render the private half into
config/management.rendered.json on every run, next to every other secret. The
recipient belongs in the template because encryption needs it; the identity is
only ever needed by a human performing a restore.`, breakGlassField)
	}
	// Guard against the assertion silently passing because the file moved.
	if !strings.Contains(string(body), "backup_recipient") {
		t.Fatal("the template no longer mentions backup_recipient - this test is checking the wrong file")
	}
}
