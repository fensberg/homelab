package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

	// Every tracked fixture and Go file, wherever it is. This walked the two
	// directories that held them when it was written; a fixture that moves, or
	// a new place to keep one, is then one nothing reads.
	var checked int
	for _, rel := range tracked(t, func(rel string) bool {
		return strings.HasSuffix(rel, ".json") || strings.HasSuffix(rel, ".go")
	}) {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
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
	}

	if checked == 0 {
		t.Fatal("no untrusted zone is named in any fixture or test, so this test proves nothing.\n\n" +
			"Either the zones moved or the shapes they are written in changed.")
	}
}

// The one place the repository says where it lives is the source its own
// Flux reads it from, found by what it declares and not by where it is. A
// fork changes that line and nothing else to become its own repository.
var ownURL = regexp.MustCompile(`(?m)^\s+url:\s*https://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?\s*$`)

// ownAddress is the repository's own owner/name, read from that line.
func ownAddress(t *testing.T) string {
	t.Helper()
	owner, name := ownOwnerAndName(t)
	return owner + "/" + name
}

func ownOwnerAndName(t *testing.T) (owner, name string) {
	t.Helper()
	path, body := fluxObject(t, "GitRepository", "flux-system")
	m := ownURL.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("%s names no repository on GitHub, so the owner whose name this looks for is unknown", path)
	}
	return m[1], m[2]
}

// The owner's name is in the repository only as part of the repository's own
// address (#540).
//
// WHY THIS EXISTS. A fork changes where it lives - its address, its images,
// its licence - and expects that to be all. The owner's name anywhere else is
// a name a fork inherits as its own without knowing: it was in a label key on
// every application's namespace, in an annotation a program read, and in the
// name every code-scanning finding was filed under. The check that searched
// for real names read them from the vault, so it ran only against a live
// estate, and none of those was ever seen.
//
// This needs no name written down. The repository says where it lives in one
// line, and the owner named there may appear elsewhere only as that address -
// owner/name, as a URL or a registry path has it - or in a place that says
// why it carries it. Anything else is refused, in every tracked file.
func TestTheOwnersNameIsOnlyInTheRepositorysAddress(t *testing.T) {
	owner, name := ownOwnerAndName(t)
	for _, p := range ownerOutsideItsAddress(owner, name, readTracked(t), ownerBelongs, formerKeys(owner)) {
		t.Error(p)
	}
}

// ownerBelongs is where the owner's name is meant to be, each with the reason
// a fork would expect to change it there. A prefix ending in a slash is a
// directory.
var ownerBelongs = map[string]string{
	"LICENSE":                  "the copyright holder, which a fork replaces with its own",
	"docs/epochs/":             "the record of what happened, which quotes the accounts and Apps it happened to",
	"commitlint.config.js":     "the App's account, in the rule that reads who co-authored a commit",
	"tests/js/unit/commitlint": "the same account, in that rule's tests",
}

// formerKeys is the one thing still in the cluster under the owner's name:
// the label key applications' namespaces had. It is kept beside the neutral
// one until every site runs a release that sets the neutral one - the
// tunnel's policy is reconciled from main and a namespace is labelled by a
// release, and between the two a tunnelled route would be refused. Built
// from the owner, so the name is not written here either; wherever the key
// is still set or selected on, it is this exact key and nothing looser.
// This goes when the label does.
func formerKeys(owner string) []string {
	return []string{"homelab." + strings.ToLower(owner) + ".com/workload"}
}

func readTracked(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, rel := range trackedFiles(t) {
		out[rel] = readRepoFile(t, rel)
	}
	return out
}

// ownerOutsideItsAddress is every line that carries the owner's name other
// than as owner/name, outside the places that say why they carry it.
func ownerOutsideItsAddress(owner, name string, files map[string]string, belongs map[string]string, former []string) []string {
	lower := strings.ToLower(owner)
	address := lower + "/" + strings.ToLower(name)
	var out []string
	for rel, body := range files {
		exempt := false
		for place := range belongs {
			if rel == place || strings.HasPrefix(rel, place) {
				exempt = true
			}
		}
		if exempt || !strings.Contains(strings.ToLower(body), lower) {
			continue
		}
		for i, line := range strings.Split(body, "\n") {
			l := strings.ReplaceAll(strings.ToLower(line), address, "")
			for _, key := range former {
				l = strings.ReplaceAll(l, key, "")
			}
			if strings.Contains(l, lower) {
				out = append(out, fmt.Sprintf("%s:%d carries the owner's name outside the repository's own address. A fork would inherit it as its own: use a name that is nobody's, or read the owner from the run.", rel, i+1))
			}
		}
	}
	sort.Strings(out)
	return out
}

func TestOwnerOutsideItsAddressFindsTheNameAndNotTheAddress(t *testing.T) {
	belongs := map[string]string{"NOTICE": "the holder", "history/": "what happened"}
	for label, tc := range map[string]struct {
		rel, body string
		found     bool
	}{
		"the address":                  {"a.yaml", "url: https://github.com/Acme/Yard\n", false},
		"a registry path":              {"a.yaml", "image: ghcr.io/acme/yard-thing@sha256:abc\n", false},
		"a label key":                  {"a.tf", `"yard.acme.com/workload" = x`, true},
		"a product name":               {"a.go", `"name": "Acme Clerk"`, true},
		"the address and the name":     {"a.md", "acme/yard is run by Acme\n", true},
		"another repository of theirs": {"a.yaml", "url: https://github.com/acme/other\n", true},
		"a place that says why":        {"NOTICE", "Copyright Acme\n", false},
		"under a place that says why":  {"history/2026.md", "the acme-bot account\n", false},
		"nothing of theirs":            {"a.go", "package a\n", false},
	} {
		got := ownerOutsideItsAddress("Acme", "Yard", map[string]string{tc.rel: tc.body}, belongs, []string{"old.acme.example/thing"})
		if (len(got) > 0) != tc.found {
			t.Errorf("%s: %v", label, got)
		}
	}
}
