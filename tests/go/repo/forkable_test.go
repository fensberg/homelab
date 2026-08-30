package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// This repository is meant to be forkable, so no estate's own names may be
// committed to it. The first version of this check held a list of those names -
// which put them in the repository permanently, in plaintext, inside the file
// whose job was keeping them out. A denylist of secrets cannot live in the
// thing it protects.
//
// So the check is split. Here, hermetically and with no names in it, a shape:
// control-plane VM names are derived from the site's own name, which makes
// them the likeliest thing to be pasted out of a terminal into a fixture or an
// example. Anything of that shape must use a documented placeholder.
//
// The complete check - the real names, read from the vault-backed rendered
// config and searched for across the tree - lives in tests/go/integration,
// where credentials already exist and nothing has to be written down.

// Matches a control-plane VM name: <site>-cp-NN.
var vmNamePattern = regexp.MustCompile(`\b([a-z][a-z0-9-]*)-cp-\d+\b`)

// Placeholder site names that examples and fixtures may use. RFC 5737 does
// this for addresses; there is no equivalent registry for names, so the
// repository keeps its own short list.
var placeholderSites = map[string]bool{
	"example":             true,
	"north-street-office": true,
	"redacted":            true,
}

// Positional keys - site0, site10 - are the config's own map keys and carry no
// information about a real place, so examples may use them freely.
var positionalSite = regexp.MustCompile(`^site\d+$`)

func isPlaceholderSite(name string) bool {
	return placeholderSites[name] || positionalSite.MatchString(name)
}

func TestVMNameExamplesUseAPlaceholderSite(t *testing.T) {
	walkText(t, func(rel, body string) {
		if strings.HasSuffix(rel, "forkable_test.go") {
			return // this file contains the pattern in order to describe it
		}
		for _, m := range vmNamePattern.FindAllStringSubmatch(strings.ToLower(body), -1) {
			if !isPlaceholderSite(m[1]) {
				t.Errorf("%s names a control-plane VM as %q. VM names are derived from the site's own name, which belongs in the vault - use one of the documented placeholders instead.", rel, m[0])
			}
		}
	})
}

var textFileExts = map[string]bool{
	".go": true, ".md": true, ".yml": true, ".yaml": true, ".tf": true,
	".json": true, ".sh": true, ".hcl": true, ".ts": true, ".js": true,
}

var skipWalkDirs = map[string]bool{
	".git": true, "node_modules": true, ".terraform": true, "coverage": true,
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
