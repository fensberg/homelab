// Package encryption_test rehearses the OpenTofu state-encryption cutover.
//
// Turning encryption on over state that already exists is the one change in
// this repository that can make the estate unrecoverable in a single step: get
// it wrong and the state describing what to destroy becomes unreadable. It is
// also, normally, the kind of thing you would rehearse on a disposable estate
// - and there is not going to be one.
//
// So the rehearsal was shrunk until it fits here. The behaviour being tested
// lives in OpenTofu's state-serialization layer, above the backend, so a
// scratch workspace with a terraform_data resource exercises exactly the same
// machinery the real cutover will. It was additionally run by hand once
// against a throwaway Postgres in Docker, using the pg backend the real state
// uses, and behaved identically - the database row went from readable JSON to
// an encrypted blob.
//
// This runs hermetically: no credentials, no network beyond the provider
// OpenTofu already has, nothing outside a temp directory.
package encryption_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The value planted in state. If this string is readable in the state file,
// the state is not encrypted - which is the whole assertion.
const canary = "PLAINTEXT-CANARY-VALUE"

const passphrase = "rehearsal-passphrase-at-least-sixteen-chars"

const mainTF = `
resource "terraform_data" "secret_bearing" {
  input = "` + canary + `"
}
`

// The migration configuration. Note method "unencrypted" "migrate" - an empty
// `fallback {}` does NOT parse, it is rejected as an invalid expression, and
// the fallback must name an explicit unencrypted method. That was found by
// running it, and it is the kind of detail that turns a planned migration into
// an incident.
const withFallback = `
terraform {
  encryption {
    method "unencrypted" "migrate" {}

    key_provider "pbkdf2" "primary" {
      passphrase = "` + passphrase + `"
    }
    method "aes_gcm" "primary" {
      keys = key_provider.pbkdf2.primary
    }

    state {
      method = method.aes_gcm.primary
      fallback {
        method = method.unencrypted.migrate
      }
    }
  }
}
`

const withoutFallback = `
terraform {
  encryption {
    key_provider "pbkdf2" "primary" {
      passphrase = "` + passphrase + `"
    }
    method "aes_gcm" "primary" {
      keys = key_provider.pbkdf2.primary
    }
    state {
      method = method.aes_gcm.primary
    }
  }
}
`

func tofu(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	c := exec.Command("tofu", args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	return string(out), err
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func stateFile(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "terraform.tfstate"))
	if err != nil {
		t.Fatalf("reading state: %v", err)
	}
	return string(b)
}

func TestStateEncryptionCutover(t *testing.T) {
	if _, err := exec.LookPath("tofu"); err != nil {
		t.Skip("tofu is not on PATH; this rehearsal needs it")
	}
	dir := t.TempDir()
	write(t, dir, "main.tf", mainTF)

	// --- where we are today: state exists, unencrypted --------------------
	if out, err := tofu(t, dir, "init", "-input=false"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := tofu(t, dir, "apply", "-auto-approve", "-input=false"); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if !strings.Contains(stateFile(t, dir), canary) {
		t.Fatal("the canary is not readable in unencrypted state - the test's premise is broken, not the code")
	}
	unencrypted := stateFile(t, dir)

	// --- step one: encryption on, fallback present ------------------------
	write(t, dir, "encryption.tf", withFallback)
	if out, err := tofu(t, dir, "init", "-input=false"); err != nil {
		t.Fatalf("init with fallback: %v\n%s\n\nIf this complains about an invalid expression, the fallback syntax changed - it needs an explicit unencrypted method, not an empty block.", err, out)
	}
	if out, err := tofu(t, dir, "apply", "-auto-approve", "-input=false", "-refresh-only"); err != nil {
		t.Fatalf("re-encrypting apply: %v\n%s", err, out)
	}
	if strings.Contains(stateFile(t, dir), canary) {
		t.Fatal("state is still readable after the migration apply - the cutover did not take")
	}

	// --- step two: fallback removed ---------------------------------------
	write(t, dir, "encryption.tf", withoutFallback)
	if out, err := tofu(t, dir, "init", "-input=false"); err != nil {
		t.Fatalf("init without fallback: %v\n%s", err, out)
	}
	if out, err := tofu(t, dir, "plan", "-input=false"); err != nil {
		t.Fatalf("encrypted state must still be readable once the fallback is gone: %v\n%s", err, out)
	}

	// --- and the lock actually locks --------------------------------------
	// Put the old unencrypted state back. With no unencrypted method
	// configured, OpenTofu must refuse it rather than silently accepting a
	// downgrade.
	write(t, dir, "terraform.tfstate", unencrypted)
	out, err := tofu(t, dir, "plan", "-input=false")
	if err == nil {
		t.Fatal("unencrypted state was accepted after the fallback was removed - the migration is reversible by anyone who can write the state file")
	}
	if !strings.Contains(out, "unencrypted payload") {
		t.Errorf("expected a refusal naming the unencrypted payload, got:\n%s", out)
	}
}
