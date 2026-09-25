package phases

import (
	"fmt"

	"homelab/contractor/internal/run"
	"homelab/details/onepassword"
	"homelab/details/secrets"
	"homelab/details/stateencryption"
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
	return stateencryption.Establish(func() (string, error) {
		if err := EnsureVaultSession(); err != nil {
			return "", err
		}
		ref, err := onepassword.ParseRef(fmt.Sprintf("op://homelab/%s/database/%s", ctx.Site, stateencryption.PassphraseField))
		if err != nil {
			return "", err
		}
		passphrase, status, err := onepassword.EnsureField(ref, func() (string, error) {
			return secrets.Password(32)
		})
		if status == "generated" {
			run.Ok("generated a state encryption passphrase and stored it in 1Password")
		}
		return passphrase, err
	})
}

// NeedsStateEncryption reports whether a single-phase run has to establish the
// state-encryption passphrase, and therefore a vault session, before it starts.
//
// Almost every phase does: state is encrypted at rest, so a tofu invocation
// without TF_ENCRYPTION cannot read it, and setting it per phase would leave
// `-from cluster` and the teardown unable to reach the state they exist to
// operate on.
//
// Sterilize is the exception, and it matters. Its whole job is deleting the
// rendered secrets from a long-lived self-hosted runner, and it runs from a
// cleanup step that deliberately carries no vault token - because removing
// files should not need vault access. Requiring a session made the cleanup
// fail exactly when it was most needed: a run that gets past Render and then
// fails leaves secrets on disk, and the step that removes them could not
// start (#219).
//
// The same reasoning keeps EmergencyDestroy unattended. A recovery path that
// depends on a credential fails precisely when things have gone wrong.
func NeedsStateEncryption(phase string) bool {
	return phase != "sterilize"
}
