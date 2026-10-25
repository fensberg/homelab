package repo

import (
	"slices"
	"strings"
	"testing"
)

// Every chart's images are either governed here or declared as not governed.
//
// THE GAP THIS CLOSES. `scripts/approved-suppliers.yml` opens its registries
// section by saying an image must come from an approved registry AND be pinned
// by digest. The image rule in policy/conftest/rules/suppliers/suppliers.rego enforces that for
// image references WRITTEN IN THIS REPOSITORY, which is not the same set: an
// image a chart pulls by its own default has no reference here to read.
//
// So the OpenEBS localpv provisioner - the component handing out the volumes
// this estate's own OpenTofu state database sits on - is pulled from docker.io
// by tag, and nothing in the repository said so (#319).
//
// WHY THAT SHAPE IS THE PROBLEM RATHER THAN DOCKER HUB. "No unapproved image is
// committed here" and "no unapproved image runs here" produce the same green,
// and the prose asserted the second while the check did the first. A reader had
// no way to tell. That is the same failure this repository has already repaired
// in the coverage regime and in the egress allowlist, and the answer both times
// was the same: enumerate the subjects from the tree, and make an uncovered one
// a declared debt rather than a silence.
//
// WHY NOT SIMPLY WIDEN THE CHECK. Reading each chart's own values.yaml means
// fetching the chart, and this tier is deliberately hermetic. That belongs in a
// nightly, and it is #319's option 2. This is option 1 - make the claim honest
// and the gap countable - which the issue says should happen regardless.

type supplierChartGaps struct {
	Gaps []struct {
		Chart     string `yaml:"chart"`
		PullsFrom string `yaml:"pulls_from"`
		What      string `yaml:"what"`
		ClosedBy  string `yaml:"closed_by"`
	} `yaml:"chart_images_not_governed_here"`
}

func TestEveryChartsImagesAreGovernedOrDeclared(t *testing.T) {
	tree := repository(t)

	var declared supplierChartGaps
	tree.YAMLInto(t, "scripts/approved-suppliers.yml", &declared)
	ungoverned := map[string]bool{}
	for _, g := range declared.Gaps {
		if g.Chart == "" {
			t.Error("an entry in chart_images_not_governed_here names no chart")
			continue
		}
		if strings.TrimSpace(g.PullsFrom) == "" || strings.TrimSpace(g.ClosedBy) == "" {
			t.Errorf(`the entry for %q does not say both where it pulls from and what
would close it.

An exemption with no condition attached stops being a debt anybody intends to
pay, which is the difference this repository draws everywhere else between a
block list and an allow list.`, g.Chart)
		}
		ungoverned[g.Chart] = true
	}

	// Every chart the estate installs, discovered rather than listed.
	charts := map[string]string{}  // chart -> the file that installs it
	releases := map[string][]any{} // the file -> its releases
	for _, rel := range tree.Files(t) {
		if !strings.HasPrefix(rel, "clusters/") || !strings.HasSuffix(rel, ".yaml") {
			continue
		}
		for _, doc := range tree.YAMLDocuments(t, rel) {
			if m, _ := doc.(map[string]any); m["kind"] != kindHelmRelease {
				continue
			}
			releases[rel] = append(releases[rel], doc)
			for _, chart := range scalarsAt(doc, "chart") {
				charts[chart] = rel
			}
		}
	}

	const atLeastFourCharts = 4
	if len(charts) < atLeastFourCharts {
		t.Fatalf(`only %d chart(s) were found under clusters/, and this estate installs
more than that.

The walk has stopped matching - a directory moved, or HelmRelease is now
spelled some other way - and every chart it no longer sees is one this check
silently stopped asking about.`, len(charts))
	}

	for chart, where := range charts {
		if ungoverned[chart] {
			continue
		}
		if holdsAny(releases[where], "image", "repository", "registry") {
			continue // Its images are set here, so the walk-based check covers them.
		}
		t.Errorf(`%s installs the chart %q, which neither pins its images in this
repository nor appears in chart_images_not_governed_here.

So nothing here knows what it pulls or from where, and the registries section
of scripts/approved-suppliers.yml reads as though it did. Either set the image
registry and digest in the HelmRelease's own values - which brings them into
the suppliers rule (policy/conftest/rules/suppliers/suppliers.rego) with no new machinery,
and is what the estate already does for the runner image - or declare the gap
with what it pulls and what would close it.`, where, chart)
	}

	// And a declaration for a chart nobody installs is stale.
	for chart := range ungoverned {
		if _, installed := charts[chart]; !installed {
			t.Errorf(`chart_images_not_governed_here names %q, which no HelmRelease
installs.

A debt recorded against something that is gone is a line nobody can act on and
a number that makes the gap look larger than it is.`, chart)
		}
	}
}

// holdsAny reports whether any of the keys holds a value anywhere in parsed
// documents.
func holdsAny(node any, keys ...string) bool {
	switch v := node.(type) {
	case []any:
		for _, x := range v {
			if holdsAny(x, keys...) {
				return true
			}
		}
	case map[string]any:
		for k, x := range v {
			if x != nil && x != "" && slices.Contains(keys, k) {
				return true
			}
			if holdsAny(x, keys...) {
				return true
			}
		}
	}
	return false
}
