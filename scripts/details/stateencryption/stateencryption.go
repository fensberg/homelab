// Package stateencryption renders the OpenTofu encryption block every root in
// this repository runs under, carried in TF_ENCRYPTION so that a bare `tofu`
// without it cannot read state at all. One drawing of it, because the site
// roots and the estate root must never disagree about what "encrypted" means.
package stateencryption

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"homelab/details/console"
)

// PassphraseField is the vault field every root's passphrase is kept in.
const PassphraseField = "encryption_passphrase"

// Establish puts the encryption block into TF_ENCRYPTION, asking passphrase
// for the key, before anything runs tofu.
//
// A block already in the environment wins. Somebody is mid-cutover, or
// driving tofu by hand from the runbook, and overwriting their block here is
// how a migration loses its fallback halfway through.
func Establish(passphrase func() (string, error)) error {
	if _, set := os.LookupEnv("TF_ENCRYPTION"); set {
		console.Info("TF_ENCRYPTION is already set; leaving it alone")
		return nil
	}
	p, err := passphrase()
	if err != nil {
		return fmt.Errorf("state encryption passphrase: %w", err)
	}
	block := Block(p)
	if block == "" {
		return errors.New("the state encryption passphrase is empty")
	}
	if err := os.Setenv("TF_ENCRYPTION", block); err != nil {
		return err
	}
	console.Ok("state encryption is on: what tofu writes is ciphertext at rest")
	return nil
}

// Block renders the block TF_ENCRYPTION carries.
//
// `plan` as well as `state`: a saved plan file holds the same attributes state
// does, so encrypting one and not the other leaves the identical secrets in a
// different file. Nothing here writes plan files today, which is exactly why
// it is worth setting now rather than remembering later.
func Block(passphrase string) string {
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
