package repo

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"homelab/contractor/config"
	"homelab/details/platform"
	"homelab/details/tofufiles"
)

// A site runs the release of the platform it was given, and no other way.
//
// WHY THIS EXISTS. A site's roots build nothing; they call modules. Called by
// path, every site runs the modules as they stand on the commit being
// converged, so a change reaches every site on the merge that made it and
// there is no way to try it on one first. A release is what a merge
// publishes instead, and each site's line in management/versions.json says
// which release that site runs: merging code changes nothing a site runs
// until its line moves.
//
// That only holds if a root cannot reach its modules around the release. So
// every module a site's root calls is fetched from the registry by digest,
// the one other source is a tree the root has to be told of and that a run
// against an estate clears, nothing chooses a release by default, and every
// site there is has a line.
func TestEverySiteRootRunsItsModulesAsReleased(t *testing.T) {
	sources := tofuSources(t)
	for _, name := range config.Roots {
		root := "management/" + name
		problems, modules := unreleasedModules(root, tofufiles.In(sources, root))
		for _, p := range problems {
			t.Error(p)
		}
		if len(modules) == 0 {
			t.Errorf("%s calls no module, so either it builds something itself or this has stopped reading it", root)
		}
		// What the root calls is there to call in the checkout, and is part
		// of what a release holds: the checks read it from the one, and the
		// next release will be the other.
		release, err := platform.Read(repoRoot(t))
		if err != nil {
			t.Fatal(err)
		}
		for _, dir := range modules {
			if len(tofufiles.In(sources, dir)) == 0 {
				t.Errorf("%s calls a module at %s, and no OpenTofu is tracked there", root, dir)
			}
			if !platform.Holds(release.Holds, dir+"/main.tf") {
				t.Errorf("%s calls a module at %s, which %s does not hold, so no release would have it", root, dir, platform.Manifest)
			}
		}
	}

	pins, err := platform.Pins(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	sites, err := config.DeclaredSites(filepath.Join(repoRoot(t), "config", "management.tpl.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range unversionedSites(sites, pins) {
		t.Error(p)
	}
}

// unversionedSites is each site with no release to run, and each line that is
// no site's.
func unversionedSites(sites []string, pins map[string]platform.Pin) (problems []string) {
	declared := map[string]bool{}
	for _, site := range sites {
		declared[site] = true
		if _, ok := pins[site]; !ok {
			problems = append(problems, fmt.Sprintf("%s is a site, and %s has no line for it, so no run against it can start. Give it the release it runs.", site, platform.VersionsFile))
		}
	}
	for site := range pins {
		if !declared[site] {
			problems = append(problems, fmt.Sprintf("%s gives %s a release, and the config declares no such site. A line that is nobody's is a version somebody believes is running.", platform.VersionsFile, site))
		}
	}
	sort.Strings(problems)
	return problems
}

var (
	moduleSourceLine = regexp.MustCompile(`(?m)^\s*source\s*=\s*(.+?)\s*$`)
	// The one form a site root's module source takes: the release, fetched
	// from the registry by its digest; or, where the root was told of one, a
	// tree holding the same module as it is in a checkout.
	releasedSource = regexp.MustCompile(`^var\.unreleased != "" \? "\$\{var\.unreleased\}/([^"$?]+)" : "oci://\$\{var\.release\}//([^"$?]+)\?digest=\$\{var\.digest\}"$`)
	sourceVariable = regexp.MustCompile(`(?s)variable\s+"(release|digest|unreleased)"\s*\{(.*?)\n\}`)
	defaultLine    = regexp.MustCompile(`(?m)^\s*default\s*=\s*(.+?)\s*$`)
)

// unreleasedModules is each way one root reaches its modules around the
// release, and the repository-relative directory of each module it calls
// properly.
func unreleasedModules(root string, files map[string]string) (problems, modules []string) {
	declared := map[string]bool{}
	for rel, body := range files {
		code := tofufiles.Code(body)
		for _, m := range moduleSourceLine.FindAllStringSubmatch(code, -1) {
			released := releasedSource.FindStringSubmatch(m[1])
			if released == nil {
				problems = append(problems, fmt.Sprintf("%s calls a module from %s. A site's root fetches every module from the release its site was given - oci://${var.release}//<the module's path>?digest=${var.digest} - or, for a check, reads the same path under ${var.unreleased}. Any other source runs something no site's line chose.", rel, m[1]))
				continue
			}
			if released[1] != released[2] {
				problems = append(problems, fmt.Sprintf("%s calls %s when checked and %s when released, so the check is of a module no site runs.", rel, released[1], released[2]))
				continue
			}
			modules = append(modules, filepath.ToSlash(filepath.Clean(released[1])))
		}
		for _, m := range sourceVariable.FindAllStringSubmatch(code, -1) {
			declared[m[1]] = true
			if d := defaultLine.FindStringSubmatch(m[2]); d != nil && d[1] != `""` {
				problems = append(problems, fmt.Sprintf("%s gives %q a default of %s. A root that was not told which release to run must run none; a default is a version nobody chose.", rel, m[1], d[1]))
			}
		}
	}
	for _, name := range []string{"release", "digest", "unreleased"} {
		if !declared[name] {
			problems = append(problems, fmt.Sprintf("%s declares no %q variable, so nothing tells it where its modules come from.", root, name))
		}
	}
	sort.Strings(problems)
	sort.Strings(modules)
	return problems, modules
}

func TestUnreleasedModulesRefusesEveryWayAroundTheRelease(t *testing.T) {
	variable := func(name, def string) string {
		return "variable \"" + name + "\" {\n  type    = string\n  default = " + def + "\n}\n"
	}
	vars := variable("release", `""`) + variable("digest", `""`) + variable("unreleased", `""`)
	call := func(source string) string { return "module \"m\" {\n  source = " + source + "\n}\n" }
	released := call(`var.unreleased != "" ? "${var.unreleased}/parts/thing" : "oci://${var.release}//parts/thing?digest=${var.digest}"`)
	problems, modules := unreleasedModules("ground/alpha", map[string]string{"ground/alpha/calls.tf": vars + released})
	if len(problems) != 0 || strings.Join(modules, " ") != "parts/thing" {
		t.Fatalf("a root fetching its module as released: %v, %v", problems, modules)
	}
	for name, tc := range map[string]struct{ body, want string }{
		"by path":                         {vars + call(`"../../parts/thing"`), "calls a module from"},
		"from a registry":                 {vars + call(`"example/thing/x"`), "calls a module from"},
		"from git":                        {vars + call(`"git::https://example.invalid/r.git//parts/thing?ref=main"`), "calls a module from"},
		"by a version name":               {vars + call(`"oci://${var.release}//parts/thing?tag=v2026.10.1"`), "calls a module from"},
		"a fixed digest":                  {vars + call(`"oci://${var.release}//parts/thing?digest=sha256:abc"`), "calls a module from"},
		"a fixed registry":                {vars + call(`var.unreleased != "" ? "${var.unreleased}/parts/thing" : "oci://registry.invalid/r//parts/thing?digest=${var.digest}"`), "calls a module from"},
		"a different module when checked": {vars + call(`var.unreleased != "" ? "${var.unreleased}/parts/other" : "oci://${var.release}//parts/thing?digest=${var.digest}"`), "when checked and"},
		"a default checkout":              {variable("release", `""`) + variable("digest", `""`) + variable("unreleased", `"../.."`) + released, `gives "unreleased" a default`},
		"a default digest":                {variable("release", `""`) + variable("digest", `"sha256:abc"`) + variable("unreleased", `""`) + released, `gives "digest" a default`},
		"no digest variable":              {variable("release", `""`) + variable("unreleased", `""`) + released, `declares no "digest" variable`},
		"a commented-out source":          {vars + released + "#   source = \"../../parts/thing\"\n", ""},
	} {
		problems, _ := unreleasedModules("ground/alpha", map[string]string{"ground/alpha/calls.tf": tc.body})
		got := strings.Join(problems, "|")
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

func TestUnversionedSitesWantsALineForEverySiteAndNoOther(t *testing.T) {
	pin := platform.Pin{Version: "v2026.10.1", Digest: "sha256:abc"}
	if got := unversionedSites([]string{"alpha", "beta"}, map[string]platform.Pin{"alpha": pin, "beta": pin}); len(got) != 0 {
		t.Errorf("every site with its line: %v", got)
	}
	got := strings.Join(unversionedSites([]string{"alpha", "beta"}, map[string]platform.Pin{"alpha": pin, "gamma": pin}), "|")
	for _, want := range []string{"beta is a site", "gives gamma a release"} {
		if !strings.Contains(got, want) {
			t.Errorf("got %q, want it to say %q", got, want)
		}
	}
}
