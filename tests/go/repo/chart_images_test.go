package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Every chart's images are either governed here or declared as not governed.
//
// THE GAP THIS CLOSES. `scripts/approved-suppliers.yml` opens its registries
// section by saying an image must come from an approved registry AND be pinned
// by digest. `TestClusterImagesComeFromApprovedRegistriesAndArePinned` enforces
// that for image references WRITTEN IN THIS REPOSITORY, which is not the same
// set: an image a chart pulls by its own default has no reference here to read.
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

var (
	helmChartName = regexp.MustCompile(`(?m)^[^\S\n]*chart:[^\S\n]+(\S+)[^\S\n]*$`)
	imageInValues = regexp.MustCompile(`(?m)^\s*(?:image|repository|registry):\s*\S`)
)

func TestEveryChartsImagesAreGovernedOrDeclared(t *testing.T) {
	root := repoRoot(t)

	var declared supplierChartGaps
	body := readRepoFile(t, "scripts/approved-suppliers.yml")
	if err := yaml.Unmarshal([]byte(body), &declared); err != nil {
		t.Fatalf("parsing scripts/approved-suppliers.yml: %v", err)
	}
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
	charts := map[string]string{} // chart -> the file that installs it
	err := filepath.WalkDir(filepath.Join(root, "clusters"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".yaml") {
			return err
		}
		manifest, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := string(manifest)
		if !strings.Contains(text, "kind: HelmRelease") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		for _, m := range helmChartName.FindAllStringSubmatch(text, -1) {
			charts[m[1]] = rel
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking clusters/ for HelmReleases: %v", err)
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
		manifest := readRepoFile(t, where)
		if imageInValues.MatchString(manifest) {
			continue // Its images are set here, so the walk-based check covers them.
		}
		t.Errorf(`%s installs the chart %q, which neither pins its images in this
repository nor appears in chart_images_not_governed_here.

So nothing here knows what it pulls or from where, and the registries section
of scripts/approved-suppliers.yml reads as though it did. Either set the image
registry and digest in the HelmRelease's own values - which brings them into
TestClusterImagesComeFromApprovedRegistriesAndArePinned with no new machinery,
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

// Every Flux controller the generated manifest ships is pinned by this overlay.
//
// `gotk-components.yaml` is written wholesale by `flux bootstrap` and pins its
// controllers by TAG. The kustomization beside it replaces each tag with a
// digest, and the transformer matches on image NAME - so a controller the
// overlay does not name keeps its tag and runs unpinned.
//
// `task render-flux-digests` now reads the tags out of the generated manifest
// rather than a list, so a new controller is picked up automatically (#104).
// This is the half that fails when it is not: a controller added upstream, or
// one renamed, goes from pinned to mutable with the manifest still building and
// every other check still green.
func TestEveryFluxControllerIsPinnedHere(t *testing.T) {
	components := readRepoFile(t, "clusters/management/flux-system/gotk-components.yaml")
	overlay := readRepoFile(t, "clusters/management/flux-system/kustomization.yaml")

	shipped := map[string]bool{}
	for _, m := range regexp.MustCompile(`ghcr\.io/fluxcd/([a-z-]+):v[0-9.]+`).
		FindAllStringSubmatch(components, -1) {
		shipped[m[1]] = true
	}

	const atLeastFourControllers = 4
	if len(shipped) < atLeastFourControllers {
		t.Fatalf(`found %d controller image(s) in gotk-components.yaml, and Flux ships
at least four.

The parse has stopped matching, so a controller running unpinned would be
invisible to this - which is the same green as everything being pinned.`, len(shipped))
	}

	for name := range shipped {
		if !strings.Contains(overlay, "ghcr.io/fluxcd/"+name+"\n") &&
			!strings.Contains(overlay, "ghcr.io/fluxcd/"+name+" ") {
			t.Errorf(`gotk-components.yaml ships ghcr.io/fluxcd/%s and the kustomization
does not pin it.

kustomize's image transformer matches on NAME, so a controller it does not name
keeps the mutable tag the generated manifest carries - on the component that
applies everything else in the cluster. The overlay still builds and nothing
else objects.

Run: task render-flux-digests`, name)
		}
	}
}
