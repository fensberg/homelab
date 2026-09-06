//go:build integration

package integration

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
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

	// The organization is deliberately NOT checked, and this is the one
	// exclusion in the whole guard, so it is worth the paragraph.
	//
	// It was checked, and reported sixteen tracked files on every run. Every
	// occurrence was this project's own identity rather than an estate secret:
	// the repository slug in the Flux git source, in CODEOWNERS and in the
	// GITHUB_REPOSITORY that a dozen tests set; the bot's own account name in
	// commitlint's rule and the records describing it; the digest-pinned
	// runner image at ghcr.io/<org>/homelab-runner; and the LICENSE copyright
	// holder. None can be removed without breaking the thing that names them,
	// and the image is pinned by digest precisely because a mutable tag is a
	// pointer somebody can move.
	//
	// It is also not a secret in the first place. The organization is in the
	// clone URL: anyone reading this file already has it. What a fork must
	// replace is the site and the hypervisor, and those are what the vault
	// holds and what a pasted terminal transcript leaks - which is the failure
	// this guard exists for, and the failure it still catches.
	//
	// So the rule narrowed to what is true rather than accumulating sixteen
	// exemptions each answering "never, it is in the clone URL". See
	// docs/epochs/02-abstraction.md.
	add(site.Name)
	for _, node := range site.Hypervisor.Nodes {
		add(node.Hostname)
	}

	if len(names) == 0 {
		t.Fatal("the rendered config yielded no names to check, which means this test is not testing anything")
	}

	root := harness.RepoRoot(t)
	exts := map[string]bool{
		".go": true, ".md": true, ".yml": true, ".yaml": true, ".tf": true,
		".json": true, ".sh": true, ".hcl": true, ".ts": true, ".js": true,
	}

	// Asked of git, not of the filesystem.
	//
	// The question is "is this committed", and a tree walk answers "is this on
	// disk" - a different thing during a run that has just rendered secrets.
	// management/hypervisor/inventory.yml is written by the Render phase,
	// removed by Sterilize, gitignored and untracked, and holds the
	// hypervisor's hostname and address because that is what it is for. The
	// walk reported it as committed on every run.
	//
	// This also retires the two path exemptions that used to be needed for
	// config/management.rendered.json and its placeholder. They were the same
	// false positive found twice, and a third rendered artifact would have
	// been found a third time. Nothing has to be listed now.
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Fatalf("listing tracked files: %v", err)
	}
	tracked := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	if len(tracked) < 100 {
		t.Fatalf("git reported only %d tracked files, which is too few to be the repository - this test would pass by looking at almost nothing", len(tracked))
	}

	for _, rel := range tracked {
		if rel == "" || !exts[strings.ToLower(filepath.Ext(rel))] {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, rel))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// Tracked but not on disk is a deleted-but-staged state, and not
			// something this test has an opinion about. This is the ONLY read
			// failure that is benign.
			continue
		case err != nil:
			// Everything else is reported rather than skipped.
			//
			// Skipping is the tempting shape, because the benign case above is
			// the one that actually happens. But a permission problem or an
			// I/O error would then become a file quietly not searched, and a
			// guard that silently searches fewer files still reports clean -
			// which is the failure this repository refuses everywhere else, a
			// disabled check indistinguishable from a passing one.
			//
			// So the benign error is named rather than the whole class being
			// swallowed under a comment that is true of only one of them.
			t.Errorf("could not read tracked file %s: %v\n\nThis file was not searched, so a name in it would not have been found. Fix the read rather than ignoring it.", rel, err)
			continue
		}
		lower := strings.ToLower(string(body))
		for _, name := range names {
			if strings.Contains(lower, name) {
				// Deliberately does not print the name: this failure is read
				// in CI logs, which for a public repository are public.
				t.Errorf("%s contains one of this estate's own names. It belongs in the vault, not in git - redact it or use a documented placeholder.", rel)
				break
			}
		}
	}
}

// dnsForm mirrors the sanitising in scripts/contractor/internal/config: lowercase,
// every run of non-alphanumerics to a hyphen, trimmed.
func dnsForm(s string) string {
	s = strings.ToLower(s)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}
