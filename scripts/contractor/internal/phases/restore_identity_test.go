package phases

import (
	"os"
	"strings"
	"testing"
)

// The break-glass identity arrives on stdin because no site's token can read
// it. What arrives is held to being an age identity, so an empty pipe - an
// `op read` that failed and printed nothing - is refused with the command that
// would work, rather than handed to age to fail on obscurely.
func TestRestoreTakesOnlyAnAgeIdentityFromStdin(t *testing.T) {
	got, err := identityFrom(strings.NewReader("AGE-SECRET-KEY-1FIXTURE\n"))
	if err != nil || !strings.Contains(string(got), "AGE-SECRET-KEY-1FIXTURE") {
		t.Fatalf("a piped identity was not taken: %q, %v", got, err)
	}
	for _, bad := range []string{"", "\n", "[ERROR] item not found"} {
		_, err := identityFrom(strings.NewReader(bad))
		if err == nil {
			t.Errorf("%q was taken as an identity", bad)
			continue
		}
		if !strings.Contains(err.Error(), "op read "+BackupIdentityRef) {
			t.Errorf("the refusal does not give the command that works: %v", err)
		}
	}
	if !strings.HasPrefix(BackupIdentityRef, "op://estate/") {
		t.Errorf("the identity is at %s, not in the estate's own vault, which is the one no site can read", BackupIdentityRef)
	}
}

// A pipe on stdin is read; that is how a restore is run.
func TestRestoreReadsTheIdentityFromAPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = w.WriteString("AGE-SECRET-KEY-1FIXTURE\n")
		w.Close()
	}()
	got, err := readIdentity(r)
	if err != nil || !strings.Contains(string(got), "AGE-SECRET-KEY-1FIXTURE") {
		t.Fatalf("got %q, %v", got, err)
	}
}

// What the contractor generates for a site is the site's, so it is written to
// the site's own vault - never a -shared one, which the lawyer writes and
// other readers trust, and never the estate's, which no site can see.
func TestGeneratedSecretsLandInTheSitesOwnVault(t *testing.T) {
	for name, ref := range map[string]string{
		"world backup key": WorldBackupKeyRef("site0"),
	} {
		if !strings.HasPrefix(ref, "op://site0/") {
			t.Errorf("the %s is written to %s, not the site's own vault", name, ref)
		}
	}
}
