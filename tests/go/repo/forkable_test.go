package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This repository is meant to be forkable: clone it, point it at your own
// vault, run it. That only holds while no estate's own names are compiled into
// it. The organization, the site and the hypervisor are op:// references in
// config/management.tpl.json precisely so they live in the vault rather than
// in git, and a fixture that hardcodes one both leaks it and breaks the fork.
//
// Scoped to the Go programs rather than the whole tree on purpose. The remote
// URL, the LICENSE and the bot identities carry the organization's name
// unavoidably; the epoch records are a written history and sanitising those
// retroactively would falsify them. Code and fixtures have no such excuse -
// they are the part somebody else runs.
func TestNoEstateNamesInProgramSources(t *testing.T) {
	root := repoRoot(t)

	// Names belonging to this particular estate. Placeholders to use instead:
	// "example" for an organization or cluster, "example-cp-01" for a VM, and
	// RFC 5737 documentation addresses (192.0.2.0/24) for IPs.
	banned := []string{"fensberg", "sheridan", "martha"}

	for _, dir := range []string{"scripts", "tests/go"} {
		err := filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			// This file names them in order to ban them.
			if strings.HasSuffix(path, "forkable_test.go") {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			lower := strings.ToLower(string(body))
			for _, name := range banned {
				if strings.Contains(lower, name) {
					t.Errorf("%s contains %q. This estate's own names belong in the vault, not in code somebody else is meant to fork - use a documentation placeholder instead.", rel, name)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}
}
