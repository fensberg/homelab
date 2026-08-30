package phases

import (
	"fmt"
	"os"
	"strings"

	"homelab/steward/internal/onepassword"
	"homelab/steward/internal/run"
	"homelab/steward/internal/secrets"
)

// EnsureStateEncryption resolves the state-encryption passphrase and puts the
// whole encryption block into TF_ENCRYPTION, before any phase runs tofu.
//
// # Why the environment and not a .tf file
//
// Nothing in git then reveals the scheme or the key, and - more to the point -
// a bare `tofu` run without the variable cannot read state at all. That is the
// lock, not a side effect of hiding the config. `tofu validate`, `tofu test`
// and every CI lane are unaffected, because none of them touch state.
//
// # Why this matters more than "the local file is deleted"
//
// The OpenTofu state IS the Postgres database, and CloudNativePG streams that
// database to object storage continuously with nothing on it but gzip. So the
// R2 credentials in a leaked state file read a continuously-refreshed,
// readable copy of everything the age backup protects, including the Talos
// PKI - and an attacker never has to touch the age file to do it. Encrypting
// the state makes the rows ciphertext, so the WAL archive is ciphertext, so
// the base backups are ciphertext, and the age dump is encrypted twice.
// docs/state-and-secret-rotation.md draws that loop in full.
//
// # Why there is no migration mode
//
// A fresh estate is encrypted from its first apply and has no unencrypted
// state to migrate. Turning encryption on over state that already exists is a
// deliberate one-time cutover with a fallback method, and it is written up as
// a runbook for a human rather than built as a flag here - it would be a code
// path used once, on an estate that no longer exists, and one that quietly
// weakens the property if it were ever left switched on.
func EnsureStateEncryption(ctx *run.Context) error {
	if _, set := os.LookupEnv("TF_ENCRYPTION"); set {
		// Somebody is mid-cutover, or driving tofu by hand from the runbook.
		// Their block wins: overwriting it here is how a migration loses its
		// fallback halfway through.
		run.Info("TF_ENCRYPTION is already set; leaving it alone")
		return nil
	}

	if err := EnsureVaultSession(); err != nil {
		return err
	}

	ref, err := onepassword.ParseRef(fmt.Sprintf("op://homelab/%s/database/encryption_passphrase", ctx.Site))
	if err != nil {
		return err
	}
	passphrase, status, err := onepassword.EnsureField(ref, func() (string, error) {
		return secrets.Password(32)
	})
	if err != nil {
		return fmt.Errorf("state encryption passphrase: %w", err)
	}
	if status == "generated" {
		run.Ok("generated a state encryption passphrase and stored it in 1Password")
	}

	cfg := encryptionConfig(passphrase)
	if cfg == "" {
		return fmt.Errorf("the state encryption passphrase at %s is empty", ref)
	}
	if err := os.Setenv("TF_ENCRYPTION", cfg); err != nil {
		return err
	}
	run.Ok("state encryption is on: what tofu writes is ciphertext at rest")
	return nil
}

// encryptionConfig renders the block TF_ENCRYPTION carries.
//
// `plan` as well as `state`: a saved plan file holds the same attributes state
// does, so encrypting one and not the other leaves the identical secrets in a
// different file. Nothing here writes plan files today, which is exactly why
// it is worth setting now rather than remembering later.
func encryptionConfig(passphrase string) string {
	if strings.TrimSpace(passphrase) == "" {
		return ""
	}
	return fmt.Sprintf(`
key_provider "pbkdf2" "primary" {
  passphrase = %s
}

method "aes_gcm" "primary" {
  keys = key_provider.pbkdf2.primary
}

state {
  method = method.aes_gcm.primary
}

plan {
  method = method.aes_gcm.primary
}
`, hclString(passphrase))
}

// hclString quotes a value for HCL. A passphrase containing a quote or a
// backslash would otherwise end the string early, and the resulting parse
// error would arrive after the credential was already in the vault - at which
// point every tofu invocation fails and the cause is a config nobody can see,
// because it only exists in an environment variable.
func hclString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}
