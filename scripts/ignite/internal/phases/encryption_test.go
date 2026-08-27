package phases

import (
	"strings"
	"testing"
)

// State encryption is the difference between "the local file was deleted" and
// "the local file was worthless". It matters more than it first looks, because
// the state IS the Postgres database, and CloudNativePG streams that database
// to object storage continuously with nothing but gzip on it. Encrypting the
// state makes the rows ciphertext, so the WAL archive is ciphertext, so the
// base backups are ciphertext - one change closes the whole chain.
//
// docs/state-and-secret-rotation.md draws the loop that motivates it.

func TestEncryptionConfig_CarriesThePassphraseAndNothingElse(t *testing.T) {
	got := encryptionConfig("s3cr3t-passphrase")

	for _, want := range []string{
		`key_provider "pbkdf2" "primary"`,
		`method "aes_gcm" "primary"`,
		"state {",
		"plan {",
		`passphrase = "s3cr3t-passphrase"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the encryption config is missing %q:\n%s", want, got)
		}
	}
}

// Step 3 of the documented cutover removes the fallback, and that removal is
// the point: with it, an unencrypted state file is still readable, so the
// property is "new writes are encrypted" rather than "unencrypted state is
// refused". A fresh estate is encrypted from its first apply and never has
// unencrypted state to fall back to, so the weaker form buys nothing here.
func TestEncryptionConfig_HasNoUnencryptedFallback(t *testing.T) {
	got := encryptionConfig("anything")
	for _, forbidden := range []string{"fallback", "unencrypted"} {
		if strings.Contains(got, forbidden) {
			t.Errorf(`the encryption config contains %q.

That makes unencrypted state readable, which is exactly what step 3 of the
cutover in docs/state-and-secret-rotation.md exists to remove. Migrating an
existing unencrypted estate is a deliberate one-time procedure run by a human
from that runbook, not a mode this program offers.

%s`, forbidden, got)
		}
	}
}

// A passphrase with a quote or a backslash in it would otherwise end the
// string early and produce an HCL parse error at the worst possible moment -
// after the credential has been written to the vault, when every subsequent
// tofu invocation fails. secrets.Password only emits letters and digits, so
// this cannot happen today; the escaping is here so that it still cannot if
// somebody sets the field by hand.
func TestEncryptionConfig_EscapesThePassphrase(t *testing.T) {
	got := encryptionConfig(`a"b\c`)
	if !strings.Contains(got, `passphrase = "a\"b\\c"`) {
		t.Errorf("the passphrase was not escaped:\n%s", got)
	}
}

func TestEncryptionConfig_RefusesAnEmptyPassphrase(t *testing.T) {
	if got := encryptionConfig(""); got != "" {
		t.Errorf("an empty passphrase must produce no config at all, got:\n%s", got)
	}
}
