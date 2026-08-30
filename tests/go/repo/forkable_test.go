package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Names belonging to this particular estate. These are proper nouns - a real
// place, a real machine - and they are op:// references in
// config/management.tpl.json precisely so they live in the vault rather than
// in git.
//
// Unlike a credential, a name has no shape. It is not high-entropy, it is not
// a key format, and no vendor API can verify it, so every scanner this
// repository runs is blind to it: gitleaks, TruffleHog, the detect-private-key
// hook and Checkov's entropy checks all look for the shape of a secret. This
// list is the only thing that knows these particular words matter, which is
// why the list exists rather than a cleverer check.
var estateNames = []string{"sheridan", "martha"}

// The organization's name is different in kind. It is the repository's own
// public identity - it is in the remote URL, the LICENSE and the bot accounts,
// and redacting it would break every runnable command in the documentation. So
// it is banned where it would break a fork rather than everywhere.
const orgName = "fensberg"

var textFileExts = map[string]bool{
	".go": true, ".md": true, ".yml": true, ".yaml": true, ".tf": true,
	".json": true, ".sh": true, ".hcl": true, ".ts": true, ".js": true,
}

var skipWalkDirs = map[string]bool{
	".git": true, "node_modules": true, ".terraform": true, "coverage": true,
}

// An estate name must appear nowhere in the repository at all.
//
// Scoping the first version of this check to Go sources is what let the names
// sit in an epoch record and a comment in variables.tf: a guard that covers
// the place somebody already looked is not a guard. Documentation is where the
// temptation is strongest, because a real hostname makes an example read
// better - and that is exactly the value that must not be committed.
func TestNoEstateNamesAnywhere(t *testing.T) {
	walkText(t, func(rel string, body string) {
		if strings.HasSuffix(rel, "forkable_test.go") {
			return // this file names them in order to ban them
		}
		lower := strings.ToLower(body)
		for _, name := range estateNames {
			if strings.Contains(lower, name) {
				t.Errorf("%s contains %q. Site and hypervisor names live in the vault, never in git - redact it, or use a documentation placeholder.", rel, name)
			}
		}
	})
}

// The organization's name must not reach the program sources, because those
// are the part a fork runs unchanged.
func TestNoOrgNameInProgramSources(t *testing.T) {
	root := repoRoot(t)
	for _, dir := range []string{"scripts", "tests/go"} {
		err := filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			if strings.HasSuffix(rel, "forkable_test.go") {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(strings.ToLower(string(body)), orgName) {
				t.Errorf("%s contains %q. Someone forking this runs these sources unchanged; the organization belongs in the vault-backed config, not compiled in.", rel, orgName)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}
}

func walkText(t *testing.T, check func(rel, body string)) {
	t.Helper()
	root := repoRoot(t)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skipWalkDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !textFileExts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		check(rel, string(body))
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
}
