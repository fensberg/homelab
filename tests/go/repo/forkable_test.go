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

// A resource address is the other place a proper noun hides, and the check
// above cannot see it.
//
// `for_each` over a map from the config keys each instance by that map's key,
// so a resource iterating the hypervisor map prints
// `proxmox_virtual_environment_vm.talos_template["<the real name>"]` - a vault
// value, in an address, with no attribute printed at all. Three leaks of that
// exact shape have happened: a converge printing the site name in a resource
// description, `plan` streaming for_each keys into a public log, and a real
// hypervisor name sitting in a test fixture on main - in the same file as the
// tests written to catch the first two.
//
// The check above matches `<site>-cp-NN`, which those addresses do not look
// like, so nothing saw any of them. This one matches the shape instead: a
// quoted map key on a provider resource address must be a key the config
// actually uses, not a name.
func TestResourceAddressKeysUseAPlaceholder(t *testing.T) {
	walkText(t, func(rel, body string) {
		if strings.HasSuffix(rel, "forkable_test.go") {
			return // this file contains the pattern in order to describe it
		}
		for _, m := range addressKeyPattern.FindAllStringSubmatch(body, -1) {
			if !isPlaceholderKey(m[1]) {
				t.Errorf("%s keys a resource address by %q. for_each keys come from the "+
					"config, so a name here is a vault value published in an address - "+
					"use a positional key such as \"node0\", or a numeric one.", rel, m[0])
			}
		}
	})
}

// A provider resource address with a quoted map key:
// proxmox_virtual_environment_vm.talos_cp["100"], and the data. form of it.
// Scoped to provider-prefixed types on purpose - a bare foo.bar["x"] is
// ordinary map indexing in Go and is not what leaks.
var addressKeyPattern = regexp.MustCompile(
	`(?:proxmox|talos|tailscale|cloudflare|kubernetes|helm|local|random|null)_[a-z0-9_]*\.[a-z0-9_]+\["([^"]+)"\]`)

// Keys the config genuinely produces and that carry no proper noun: the
// positional hypervisor and site keys, and the numeric host octets. Anything
// else is refused rather than allowed, so a key nobody considered fails closed.
var positionalKey = regexp.MustCompile(`^(node|site)\d+$|^\d+$`)

func isPlaceholderKey(key string) bool {
	return positionalKey.MatchString(key) || isPlaceholderSite(key)
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
