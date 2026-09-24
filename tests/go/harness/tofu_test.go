package harness

import (
	"strings"
	"testing"
)

// The message is the whole point of this check, so it is what gets asserted.
//
// Without it, five of seven integration tests failed on 2026-09-05 with tofu's
// own wording - "Unsupported state file format: This state file is encrypted
// and can not be read without an encryption configuration" - repeated once per
// test, naming no cause and no remedy. The run read as a broken suite. It was
// a missing environment variable (#227).
func TestStateIsReadableExplainsHowToFixIt(t *testing.T) {
	t.Setenv("TF_ENCRYPTION", "")

	err := StateIsReadable()
	if err == nil {
		t.Fatal("no error when TF_ENCRYPTION is unset, so the tier would fail later with tofu's message instead of this one")
	}

	// Each of these is a thing the reader needs and tofu's own error omits.
	for _, want := range []string{
		"TF_ENCRYPTION",            // the variable
		"encrypted at rest",        // why, so it does not read like a bug
		"contractor kubeconfig",    // the command that fixes it
		"OP_SERVICE_ACCOUNT_TOKEN", // what that command itself needs
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not mention %q, so a reader still has to work it out:\n%s", want, err)
		}
	}
}

func TestStateIsReadablePassesWhenTheKeyIsPresent(t *testing.T) {
	t.Setenv("TF_ENCRYPTION", "key_provider \"pbkdf2\" \"primary\" {}")
	if err := StateIsReadable(); err != nil {
		t.Errorf("refused a run that has the encryption config: %v", err)
	}
}
