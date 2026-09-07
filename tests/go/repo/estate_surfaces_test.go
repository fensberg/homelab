package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The tiers that are hard to write get the same treatment as the ones that are
// easy: their subject is enumerated, and a surface nothing asserts about fails.
//
// WHY NOT POLICE THE DIFF. The obvious way to force higher-tier tests is to
// require that a change touching X also change a test in tier Y. It cannot
// work: nothing can tell whether a particular change needs an integration
// test, so the rule either fires on comment edits - noise, and noise is what
// gets guards switched off - or is so narrow it fires on nothing.
//
// What can be enumerated is the estate's own surface. Every workload Flux
// deploys, every vendor the estate calls, every verb the lifecycle program
// offers: each is discoverable from the repository, each is a thing a test in
// exactly one tier can see, and each is a red build until something names it.
//
// So adding a workload, a supplier, or a verb demands its test at the moment
// it is added, which is the operator's "default build them" made structural
// rather than remembered. And the bias is deliberate, in their words: "Too
// many is better than not enough. And then we do some routine cleanup now and
// again." So this asks for one per surface without trying to judge which
// surfaces deserve one - consolidating later is cheap, and noticing a gap
// years later is not.
//
// WHAT IT DOES NOT CLAIM. That a test naming a surface tests it well. Naming
// is the weakest possible assertion and it is deliberately the bar: the
// failure being prevented is a surface nobody thought about at all, not a
// shallow test. Depth is what the tier floors in tests/coverage-baseline.json
// measure, and what review is for.

// claimedBy reports whether a tier declares that it covers the surface, by
// carrying a `covers: <unit>` marker.
//
// WHY A MARKER RATHER THAN LOOKING FOR THE NAME. The first version searched
// each tier for the surface's name and got the wrong answer, because this
// repository names things by function and never by vendor: the Proxmox test is
// called TestHypervisorAPIPortServesTheAPI and contains the word "proxmox"
// nowhere. Matching on the vendor reported a tested surface as untested, and
// false debt is worse than none - it is noise, and noise is what gets a guard
// switched off.
//
// A declaration is also more honest about what is being asserted. "Some file in
// this tier mentions this string" was never evidence; "this test says it covers
// this surface" at least names a responsible test. The bar is deliberately low
// either way - what this catches is a surface nobody thought about at all.
func claimedBy(t *testing.T, dirs []string, needle string) bool {
	t.Helper()
	root := repoRoot(t)
	for _, dir := range dirs {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			continue // A tier with no directory yet names nothing.
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			body, readErr := os.ReadFile(filepath.Join(root, dir, e.Name()))
			if readErr != nil {
				t.Fatalf("reading %s/%s: %v", dir, e.Name(), readErr)
			}
			if strings.Contains(string(body), "covers: "+needle) {
				return true
			}
		}
	}
	return false
}

// deployedWorkloads is every HelmRelease under clusters/.
//
// The CloudNativePG Cluster is deliberately absent: its name is a vault value
// substituted by Flux, so it cannot be written down here, and the integration
// tier already reaches it through the label CloudNativePG puts on its pods.
func deployedWorkloads(t *testing.T) []string {
	t.Helper()
	root := repoRoot(t)
	var names []string
	for _, rel := range tracked(t, func(rel string) bool {
		return strings.HasPrefix(rel, "clusters/") && strings.HasSuffix(rel, ".yaml")
	}) {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		dec := yaml.NewDecoder(strings.NewReader(string(body)))
		for {
			var doc struct {
				Kind     string `yaml:"kind"`
				Metadata struct {
					Name string `yaml:"name"`
				} `yaml:"metadata"`
			}
			if err := dec.Decode(&doc); err != nil {
				break
			}
			if doc.Kind == "HelmRelease" && !strings.Contains(doc.Metadata.Name, "$") {
				names = append(names, doc.Metadata.Name)
			}
		}
	}
	sort.Strings(names)
	return names
}

// vendorBackedConcerns is every concern in the config that names a provider,
// plus source control, which has a vendor without a `provider` key because it
// is portable.
//
// Concerns rather than vendor names, because this repository's own rule is to
// name things by function and never by vendor - `source_control.repo_url`, not
// `git.github_repo_url`. An api test asserts that whoever serves a concern
// still behaves; which company that is belongs in the config, not the test.
func vendorBackedConcerns(t *testing.T) []string {
	template := readRepoFile(t, "config/management.tpl.json")
	declaresProvider := regexp.MustCompile(`"(\w+)":\s*\{[^{}]*"provider"`)
	seen := map[string]bool{"source_control": true}
	for _, m := range declaresProvider.FindAllStringSubmatch(template, -1) {
		seen[m[1]] = true
	}
	var out []string
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// lifecycleVerbs is what the contractor offers, read from the one list the
// program itself dispatches on.
func lifecycleVerbs(t *testing.T) []string {
	body := readRepoFile(t, "scripts/contractor/main.go")
	_, after, ok := strings.Cut(body, "var knownVerbs = []string{")
	if !ok {
		t.Fatal("scripts/contractor/main.go declares no knownVerbs, so this asserts nothing about the lifecycle")
	}
	list, _, ok := strings.Cut(after, "}")
	if !ok {
		t.Fatal("knownVerbs is not terminated, which should not compile")
	}
	var verbs []string
	for _, field := range strings.Split(list, ",") {
		if v := strings.Trim(strings.TrimSpace(field), `"`); v != "" {
			verbs = append(verbs, v)
		}
	}
	sort.Strings(verbs)
	return verbs
}

func TestEveryEstateSurfaceIsNamedByATestOrIsADeclaredDebt(t *testing.T) {
	blocked := blocklistEntries(t)

	type surface struct {
		unit  string
		dirs  []string
		what  string
		found bool
	}
	var surfaces []surface

	for _, w := range deployedWorkloads(t) {
		surfaces = append(surfaces, surface{
			unit: "integration:" + w, dirs: []string{"tests/go/integration"},
			what: "a workload Flux deploys, which only the integration tier can see running",
		})
	}
	for _, c := range vendorBackedConcerns(t) {
		surfaces = append(surfaces, surface{
			unit: "api:" + c, dirs: []string{"tests/go/api"},
			what: "a concern served by an outside vendor, whose behaviour can change without this repository changing",
		})
	}
	for _, v := range lifecycleVerbs(t) {
		surfaces = append(surfaces, surface{
			// Either tier: some verbs change the estate and need e2e, some only
			// read it and integration reaches them. Which is which is a
			// judgement, and this guard deliberately makes none.
			unit: "verb:" + v, dirs: []string{"tests/go/e2e", "tests/go/integration"},
			what: "something the lifecycle program will do to a real estate",
		})
	}
	if len(surfaces) == 0 {
		t.Fatal("discovered no estate surfaces at all, so this asserts nothing")
	}

	var unasserted, stale []string
	for _, s := range surfaces {
		named := claimedBy(t, s.dirs, s.unit)
		switch {
		case named && blocked[s.unit]:
			stale = append(stale, s.unit)
		case !named && !blocked[s.unit]:
			unasserted = append(unasserted, s.unit+"  ("+s.what+")")
		}
	}
	sort.Strings(unasserted)
	sort.Strings(stale)

	if len(unasserted) > 0 {
		t.Errorf(`%d estate surface(s) are claimed by no test in the tier that can see them:

  %s

Write one, and put "covers: <the unit above>" in its doc comment so this can
see it. It does not have to be deep - claiming the surface is the bar, because
what this catches is a surface nobody thought about at all. Too many is better
than not enough; consolidating later is cheap and noticing a gap years later
is not.

If it genuinely cannot be tested yet, add it to tests/coverage-blocklist.yml
and raise the ceiling, which puts the gap in front of somebody instead of
nowhere.`, len(unasserted), strings.Join(unasserted, "\n  "))
	}

	if len(stale) > 0 {
		t.Errorf(`%d surface(s) are on the block list and are now covered:

  %s

Remove them and lower the ceiling by the same number.`, len(stale), strings.Join(stale, "\n  "))
	}
}
