package repo

import (
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
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
	tree := repository(t)
	for _, rel := range tree.Files(t) {
		if !slices.Contains(dirs, filepath.Dir(rel)) || !strings.HasSuffix(rel, ".go") {
			continue // a tier with no directory yet names nothing
		}
		if strings.Contains(goComments(t, tree, rel), "covers: "+needle) {
			return true
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
	tree := repository(t)
	var names []string
	for _, rel := range tracked(t, func(rel string) bool {
		return strings.HasPrefix(rel, "clusters/") && strings.HasSuffix(rel, ".yaml")
	}) {
		for _, d := range tree.YAMLDocuments(t, rel) {
			var doc struct {
				Kind     string `yaml:"kind"`
				Metadata struct {
					Name string `yaml:"name"`
				} `yaml:"metadata"`
			}
			if as(d, &doc) == nil && doc.Kind == kindHelmRelease && !strings.Contains(doc.Metadata.Name, "$") {
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
	var template any
	repository(t).JSONInto(t, configTemplate, &template)
	seen := map[string]bool{"source_control": true}
	var walk func(any)
	walk = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			for k, x := range v {
				if inner, ok := x.(map[string]any); ok {
					if _, names := inner["provider"]; names {
						seen[k] = true
					}
				}
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(template)
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
	verbs := contractorVerbs(t, repository(t), "scripts/contractor/main.go")
	sort.Strings(verbs)
	return verbs
}

func TestEveryEstateSurfaceIsNamedByATestOrIsADeclaredDebt(t *testing.T) {
	t.Parallel()
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
