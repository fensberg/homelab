//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"homelab/tests/harness"
)

// The complete check that this estate's own names are not committed.
//
// It lives here rather than in the hermetic tier for one reason: the hermetic
// version had to hold the names in order to look for them, which put them in
// the repository permanently, in plaintext, inside the file whose job was
// keeping them out. A denylist of secrets cannot live in the thing it
// protects.
//
// Here the names are read at runtime from the vault-backed rendered config, so
// nothing is written down. The cost is that this runs nightly rather than on a
// pull request; tests/go/repo carries a shape check for the likeliest leak so
// the common case is still caught before merge.
func TestEstateNamesAreNotCommitted(t *testing.T) {
	cfg := harness.LoadConfig(t)
	site := harness.SiteConfig(t)

	// Everything that names a real thing. Each is an op:// reference in
	// config/management.tpl.json precisely so it stays out of git.
	var names []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if len(s) < 4 {
			return // too short to search for without matching everything
		}
		names = append(names, strings.ToLower(s))
		// VM and cluster names are the DNS-collapsed form of the site's name,
		// so search for that too - it is the form that actually leaks.
		if d := dnsForm(s); d != strings.ToLower(s) && len(d) >= 4 {
			names = append(names, d)
		}
	}

	add(cfg.Organization.Name)
	add(site.Name)
	for _, node := range site.Hypervisor.Nodes {
		add(node.Hostname)
	}

	if len(names) == 0 {
		t.Fatal("the rendered config yielded no names to check, which means this test is not testing anything")
	}

	root := harness.RepoRoot(t)
	skipDirs := map[string]bool{".git": true, "node_modules": true, ".terraform": true}
	exts := map[string]bool{
		".go": true, ".md": true, ".yml": true, ".yaml": true, ".tf": true,
		".json": true, ".sh": true, ".hcl": true, ".ts": true, ".js": true,
	}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !exts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		rel, _ := filepath.Rel(root, path)

		// The rendered config is the vault's own output. It is gitignored and
		// wiped by Sterilize; finding names in it is the point of it.
		if strings.HasPrefix(rel, "config/management.rendered.json") ||
			strings.HasPrefix(rel, "config/management.placeholder.json") {
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lower := strings.ToLower(string(body))
		for _, name := range names {
			if strings.Contains(lower, name) {
				// Deliberately does not print the name: this failure is read
				// in CI logs, which for a public repository are public.
				t.Errorf("%s contains one of this estate's own names. It belongs in the vault, not in git - redact it or use a documented placeholder.", rel)
				return nil
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
}

// dnsForm mirrors the sanitising in scripts/contractor/internal/config: lowercase,
// every run of non-alphanumerics to a hyphen, trimmed.
func dnsForm(s string) string {
	s = strings.ToLower(s)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}
