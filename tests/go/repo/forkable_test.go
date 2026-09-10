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

// The one non-positional key that is safe, and it is safe by construction:
// `<redacted>` is the literal string summarisePlan substitutes FOR a key it
// would not print. Finding it in the repository is evidence that redaction
// worked, not evidence that a name leaked - so refusing it means documentation
// cannot show what a redacted plan looks like, which is exactly the thing
// somebody reading about this guard most wants to see.
//
// Narrow on purpose: this exact string and nothing resembling it. It is
// duplicated from scripts/contractor/internal/phases/plan.go rather than
// shared, because Go's internal/ rule is per-module and this tier cannot
// import that package. If the marker there ever changes, this stops matching
// and the guard goes back to refusing the documentation - noisy, which is the
// safe direction for the two to drift in.
const redactionMarker = "<redacted>"

func isPlaceholderKey(key string) bool {
	return key == redactionMarker || positionalKey.MatchString(key) || isPlaceholderSite(key)
}

var textFileExts = map[string]bool{
	".go": true, ".md": true, ".yml": true, ".yaml": true, ".tf": true,
	".json": true, ".sh": true, ".hcl": true, ".ts": true, ".js": true,
}

var skipWalkDirs = map[string]bool{
	".git": true, "node_modules": true, ".terraform": true, "coverage": true,
}

// walkText hands every text file in the repository to a check.
//
// It counts what it visited and refuses to return having visited nothing, for
// the same reason tracked() does: the callers here are leak guards, and a leak
// guard that inspects zero files passes. There is no output that distinguishes
// "no estate name is committed anywhere" from "this walk stopped finding
// files" - both are silence and a green check - and only the first is ever the
// intended claim.
//
// The reachable version of that is not exotic. textFileExts is a fixed list, so
// a repository that moved to a different extension would be walked past;
// skipWalkDirs is a fixed list, so a directory renamed into it would vanish;
// and repoRoot resolving somewhere unexpected empties the walk entirely. None
// of those announces itself.
func walkText(t *testing.T, check func(rel, body string)) {
	t.Helper()
	root := repoRoot(t)
	visited := 0
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
		visited++
		check(rel, string(body))
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
	if visited == 0 {
		t.Fatal(`walked the repository and read no text files at all, so this test asserts nothing.

The callers of this are leak guards. Inspecting zero files passes every one of
them, and looks identical to a repository with nothing to find.`)
	}
}

// An untrusted zone is named after the workload it hosts, which makes the zone
// name the second thing likeliest to be pasted in from real life.
//
// It is not a secret in the way a site or a hypervisor name is, and nobody
// would be harmed by it. The reason it stays out is a design one: the zone is
// for untrusted work rather than for any particular workload, and a plumbing
// layer that names its first tenant is how a general capability quietly
// becomes that tenant's feature. The record names the workload, because the
// record is where the motivation belongs; the machinery should read the same
// whoever moves in next.
//
// So zone names in committed fixtures and tests use the same documented
// placeholders site names do.
func TestZoneNamesUseAPlaceholder(t *testing.T) {
	root := repoRoot(t)

	// Both shapes a zone name is written in: a config fixture's map key, and
	// the Go test fixture that builds one directly.
	inJSON := regexp.MustCompile(`"dmz_zones"\s*:\s*\{\s*"([a-z][a-z0-9-]*)"`)
	inGo := regexp.MustCompile(`DMZZone\{\s*"([a-z][a-z0-9-]*)"`)

	var checked int
	for _, dir := range []string{
		filepath.Join(root, "management", "cluster", "tests"),
		filepath.Join(root, "scripts"),
	} {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			if !strings.HasSuffix(path, ".json") && !strings.HasSuffix(path, ".go") {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			for _, pattern := range []*regexp.Regexp{inJSON, inGo} {
				for _, m := range pattern.FindAllStringSubmatch(string(body), -1) {
					checked++
					if !isPlaceholderSite(m[1]) {
						t.Errorf("%s names an untrusted zone %q.\n\n"+
							"A zone is named after the workload it hosts, and naming a real one here "+
							"makes the plumbing read as that workload's rather than as a general "+
							"capability - which is how the next tenant inherits somebody else's "+
							"assumptions. Use one of the documented placeholders; the epoch record is "+
							"where the actual workload belongs.", rel, m[1])
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}

	if checked == 0 {
		t.Fatal("no untrusted zone is named in any fixture or test, so this test proves nothing.\n\n" +
			"Either the zones moved or the shapes they are written in changed.")
	}
}
