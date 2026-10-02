package phases

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/contractor/internal/run"
	"homelab/details/applications"
	"homelab/details/onepassword"
)

// What the contractor generates for a site's applications is what each one
// declared as generated and nothing else, for the applications the site was
// given and no other, and it is written to the site's own vault - never a
// -shared one, which the lawyer writes and other readers trust, and never the
// estate's, which no site can see.
func TestApplicationSecretsAreGeneratedIntoTheSitesOwnVault(t *testing.T) {
	root := t.TempDir()
	for rel, body := range map[string]string{
		applications.SiteFilePath("site0"):                      "  path: ./" + applications.Dir + "/given/production\n",
		applications.Dir + "/given/" + applications.Declaration: `{"secrets": {"keys": {"a": {"vault": "typed"}, "b": {"generated": "backup_key"}, "c": {"generated": "signing_key"}}}}`,
		applications.Dir + "/other/" + applications.Declaration: `{"secrets": {"keys": {"b": {"generated": "not_here"}}}}`,
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var ensured []string
	ensure := func(ref onepassword.Ref, generate func() (string, error)) (string, string, error) {
		value, err := generate()
		if err != nil || len(value) != generatedSecretLength {
			t.Errorf("%s would be generated as %d characters (%v)", ref, len(value), err)
		}
		ensured = append(ensured, ref.String())
		return value, "generated", nil
	}
	if err := ensureApplicationSecrets(run.NewContext(root, "site0"), ensure); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ensured, " "); got != "op://site0/given/backup_key op://site0/given/signing_key" {
		t.Errorf("generated %s", got)
	}

	// A vault that cannot be written fails the run, naming whose field it was.
	refuse := func(onepassword.Ref, func() (string, error)) (string, string, error) {
		return "", "", errors.New("read-only vault")
	}
	if err := ensureApplicationSecrets(run.NewContext(root, "site0"), refuse); err == nil || !strings.Contains(err.Error(), "given's backup_key") {
		t.Errorf("a vault that refused the write: %v", err)
	}
}
