package repo

import (
	"fmt"
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

// A zone's key is positional for the reason a site's is: zone0 says which
// zone and nothing about who is in it. It is what the real config uses, where
// a placeholder would be a lie and a tenant's name is what this refuses.
var positionalZone = regexp.MustCompile(`^zone\d+$`)

func isPlaceholderSite(name string) bool {
	return placeholderSites[name] || positionalSite.MatchString(name)
}

func TestVMNameExamplesUseAPlaceholderSite(t *testing.T) {
	heavy(t, "semgrep reads every text file for VM names, a few seconds")
	for _, f := range scanForkable(t, `(?i)\b(?P<FULL>(?P<KEY>[a-z][a-z0-9-]*)-cp-\d+)\b`) {
		key, full, _ := strings.Cut(f.Text, "|")
		if !isPlaceholderSite(strings.ToLower(key)) {
			t.Errorf("%s names a control-plane VM as %q. VM names are derived from the site's own name, which belongs in the vault - use one of the documented placeholders instead.", f.Path, strings.ToLower(full))
		}
	}
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
	heavy(t, "semgrep reads every text file for resource addresses, a few seconds")
	for _, f := range scanForkable(t, `(?P<FULL>`+addressKeyShape+`\["(?P<KEY>[^"]+)"\])`) {
		key, full, _ := strings.Cut(f.Text, "|")
		if !isPlaceholderKey(key) {
			t.Errorf("%s keys a resource address by %q. for_each keys come from the "+
				"config, so a name here is a vault value published in an address - "+
				"use a positional key such as \"node0\", or a numeric one.", f.Path, full)
		}
	}
}

// A provider resource address with a quoted map key:
// proxmox_virtual_environment_vm.talos_cp["100"], and the data. form of it.
// Scoped to provider-prefixed types on purpose - a bare foo.bar["x"] is
// ordinary map indexing in Go and is not what leaks.
const addressKeyShape = `(?:proxmox|talos|tailscale|cloudflare|kubernetes|helm|local|random|null)_[a-z0-9_]*\.[a-z0-9_]+`

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

// scanForkable is every place a Semgrep scan of the repository's tracked text
// files finds a pattern, which names what it found as KEY and the whole of
// it as FULL; each finding's text is "KEY|FULL".
//
// It refuses to return having read nothing, for the same reason tracked()
// does: the callers here are leak guards, and a leak guard that inspects zero
// files passes. There is no output that distinguishes "no estate name is
// committed anywhere" from "this stopped finding files" - both are silence
// and a green check - and only the first is ever the intended claim.
func scanForkable(t *testing.T, pattern string) []textFinding {
	t.Helper()
	var files []string
	for _, rel := range trackedFiles(t) {
		// This file contains the patterns in order to describe them.
		if textFileExts[strings.ToLower(filepath.Ext(rel))] && filepath.Base(rel) != "forkable_test.go" {
			files = append(files, rel)
		}
	}
	if len(files) == 0 {
		t.Fatal(`found no text files at all, so this test asserts nothing.

The callers of this are leak guards. Inspecting zero files passes every one of
them, and looks identical to a repository with nothing to find.`)
	}
	rule := map[string]any{
		"id": "forkable", "languages": []string{"generic"}, "severity": "INFO",
		"message": "$KEY|$FULL", "metadata": map[string]any{"about": "forkable"},
		"pattern-regex": pattern,
	}
	return scanText(t, repository(t), []map[string]any{rule}, files)
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
	heavy(t, "semgrep reads every fixture and Go file for zone names, a few seconds")

	// Both shapes a zone name is written in: a config fixture's map key, and
	// the Go test fixture that builds one directly. Every tracked fixture and
	// Go file, wherever it is. This walked the two directories that held them
	// when it was written; a fixture that moves, or a new place to keep one,
	// is then one nothing reads.
	files := tracked(t, func(rel string) bool {
		return strings.HasSuffix(rel, ".json") || strings.HasSuffix(rel, ".go")
	})
	zone := func(id, pattern string) map[string]any {
		return map[string]any{
			"id": id, "languages": []string{"generic"}, "severity": "INFO",
			"message": "$ZONE", "metadata": map[string]any{"about": "zone"}, "pattern-regex": pattern,
		}
	}
	var checked int
	for _, f := range scanText(t, repository(t), []map[string]any{
		zone("in-json", `"dmz_zones"\s*:\s*\{\s*"(?P<ZONE>[a-z][a-z0-9-]*)"`),
		zone("in-go", `DMZZone\{\s*"(?P<ZONE>[a-z][a-z0-9-]*)"`),
	}, files) {
		checked++
		if !isPlaceholderSite(f.Text) && !positionalZone.MatchString(f.Text) {
			t.Errorf("%s names an untrusted zone %q.\n\n"+
				"A zone is named after the workload it hosts, and naming a real one here "+
				"makes the plumbing read as that workload's rather than as a general "+
				"capability - which is how the next tenant inherits somebody else's "+
				"assumptions. Use one of the documented placeholders; the epoch record is "+
				"where the actual workload belongs.", f.Path, f.Text)
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
var ownURL = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?$`)

// ownAddress is the repository's own owner/name, read from that line.
func ownAddress(t *testing.T) string {
	t.Helper()
	owner, name := ownOwnerAndName(t)
	return owner + "/" + name
}

func ownOwnerAndName(t *testing.T) (owner, name string) {
	t.Helper()
	path, docs := fluxObject(t, "GitRepository", "flux-system")
	url, _ := scalarAt(docs, "url")
	m := ownURL.FindStringSubmatch(url)
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
	heavy(t, "semgrep reads every tracked file for the owner's name, a few seconds")
	owner, name := ownOwnerAndName(t)
	// Every line of every tracked file that carries the name, found by a rule
	// built from the name read from the tree: no name is written down here.
	rule := map[string]any{
		"id": "names-the-owner", "languages": []string{"generic"}, "severity": "INFO",
		"message": "$LINE", "metadata": map[string]any{"about": "owner"},
		"pattern-regex": `(?mi)^(?P<LINE>[^\n]*` + regexp.QuoteMeta(owner) + `[^\n]*)$`,
	}
	found := scanText(t, repository(t), []map[string]any{rule}, trackedFiles(t))
	for _, p := range ownerOutsideItsAddress(owner, name, found, ownerBelongs) {
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

// ownerOutsideItsAddress is every line that carries the owner's name other
// than as owner/name, outside the places that say why they carry it.
func ownerOutsideItsAddress(owner, name string, lines []textFinding, belongs map[string]string) []string {
	lower := strings.ToLower(owner)
	address := lower + "/" + strings.ToLower(name)
	var out []string
	for _, line := range lines {
		exempt := false
		for place := range belongs {
			if line.Path == place || strings.HasPrefix(line.Path, place) {
				exempt = true
			}
		}
		if !exempt && strings.Contains(strings.ReplaceAll(strings.ToLower(line.Text), address, ""), lower) {
			out = append(out, fmt.Sprintf("%s:%d carries the owner's name outside the repository's own address. A fork would inherit it as its own: use a name that is nobody's, or read the owner from the run.", line.Path, line.Line))
		}
	}
	sort.Strings(out)
	return out
}

func TestOwnerOutsideItsAddressFindsTheNameAndNotTheAddress(t *testing.T) {
	t.Parallel()
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
		var lines []textFinding
		for i, line := range strings.Split(tc.body, "\n") {
			lines = append(lines, textFinding{Path: tc.rel, Line: i + 1, Text: line})
		}
		got := ownerOutsideItsAddress("Acme", "Yard", lines, belongs)
		if (len(got) > 0) != tc.found {
			t.Errorf("%s: %v", label, got)
		}
	}
}
