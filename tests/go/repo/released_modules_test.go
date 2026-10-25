package repo

import (
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"homelab/contractor/config"
	"homelab/details/platform"
	"homelab/tests/parsed"
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
	t.Parallel()
	tree := repository(t)
	tofu := tree.OpenTofu(t)
	var release platform.Release
	tree.JSONInto(t, platform.Manifest, &release)
	if len(release.Holds) == 0 {
		t.Fatalf("%s holds nothing, so a release of it would be an empty package", platform.Manifest)
	}
	for _, name := range config.Roots {
		root := "management/" + name
		problems, modules := unreleasedModules(t, tree, root, inDir(tofu, root))
		for _, p := range problems {
			t.Error(p)
		}
		if len(modules) == 0 {
			t.Errorf("%s calls no module, so either it builds something itself or this has stopped reading it", root)
		}
		for _, dir := range modules {
			if len(inDir(tofu, dir)) == 0 {
				t.Errorf("%s calls a module at %s, and no OpenTofu is tracked there", root, dir)
			}
			if !platform.Holds(release.Holds, dir+"/main.tf") {
				t.Errorf("%s calls a module at %s, which %s does not hold, so no release would have it", root, dir, platform.Manifest)
			}
		}
	}

	var lines map[string]platform.Line
	tree.JSONInto(t, platform.VersionsFile, &lines)
	pins := map[string]platform.Pin{}
	for site, line := range lines {
		pin, err := platform.ParsePin(line.Platform)
		if err != nil {
			t.Fatalf("%s, %s: %v", platform.VersionsFile, site, err)
		}
		pins[site] = pin
	}
	var template config.Config
	tree.JSONInto(t, "config/management.tpl.json", &template)
	sites := make([]string, 0, len(template.Sites))
	for site := range template.Sites {
		sites = append(sites, site)
	}
	if len(sites) == 0 {
		t.Fatal("the config template declares no site")
	}
	for _, p := range unversionedSites(sites, pins) {
		t.Error(p)
	}
}

// inDir is the OpenTofu files in one directory: a root's, or a module's.
func inDir(files []parsed.OpenTofuFile, dir string) []parsed.OpenTofuFile {
	var out []parsed.OpenTofuFile
	for _, f := range files {
		if path.Dir(f.Path) == dir {
			out = append(out, f)
		}
	}
	return out
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

// releasedModule is the module a call's source fetches, when the source is
// the one form a site's root may use: the module's path in the release its
// site was given, or the same path in a checkout when it is checked.
//
//	var.unreleased != "" ? "${var.unreleased}/<path>" : "oci://${var.release}//<path>?digest=${var.digest}"
//
// The path it fetches when checked and when released, each, and whether the
// source is that form at all.
func releasedModule(expr hclsyntax.Expression) (checked, released string, ok bool) {
	cond, ok := expr.(*hclsyntax.ConditionalExpr)
	if !ok {
		return "", "", false
	}
	test, ok := cond.Condition.(*hclsyntax.BinaryOpExpr)
	if !ok || test.Op != hclsyntax.OpNotEqual || !isRef(test.LHS, "var.unreleased") || !isLiteral(test.RHS, "") {
		return "", "", false
	}
	inCheckout, ok := templateParts(cond.TrueResult)
	if !ok || len(inCheckout) != 2 || inCheckout[0] != "var.unreleased" || !strings.HasPrefix(inCheckout[1], "\x00/") {
		return "", "", false
	}
	inRelease, ok := templateParts(cond.FalseResult)
	if !ok || len(inRelease) != 4 || inRelease[0] != "\x00oci://" || inRelease[1] != "var.release" || inRelease[3] != "var.digest" {
		return "", "", false
	}
	path, isPath := strings.CutPrefix(inRelease[2], "\x00//")
	path, digested := strings.CutSuffix(path, "?digest=")
	if !isPath || !digested || strings.ContainsAny(path, "?$") {
		return "", "", false
	}
	return strings.TrimPrefix(inCheckout[1], "\x00/"), path, true
}

// templateParts is a string template's parts: each reference by its name,
// and each piece of text with a NUL in front of it, so the two are never
// mistaken for each other.
func templateParts(expr hclsyntax.Expression) ([]string, bool) {
	tmpl, ok := expr.(*hclsyntax.TemplateExpr)
	if !ok {
		return nil, false
	}
	var out []string
	for _, part := range tmpl.Parts {
		if ref, ok := part.(*hclsyntax.ScopeTraversalExpr); ok {
			out = append(out, traversalName(ref.Traversal))
			continue
		}
		v, ok := parsed.Literal(part)
		if !ok || v.IsNull() || v.Type() != cty.String {
			return nil, false
		}
		out = append(out, "\x00"+v.AsString())
	}
	return out, true
}

func isRef(expr hclsyntax.Expression, name string) bool {
	ref, ok := expr.(*hclsyntax.ScopeTraversalExpr)
	return ok && traversalName(ref.Traversal) == name
}

func isLiteral(expr hclsyntax.Expression, want string) bool {
	v, ok := parsed.Literal(expr)
	return ok && !v.IsNull() && v.Type() == cty.String && v.AsString() == want
}

// unreleasedModules is each way a root's module calls fetch something other
// than the release its site was given, and the modules it does fetch.
func unreleasedModules(t testing.TB, tree *parsed.Tree, root string, files []parsed.OpenTofuFile) (problems, modules []string) {
	declared := map[string]bool{}
	for _, f := range files {
		for _, b := range f.Body.Blocks {
			switch {
			case b.Type == "module":
				source, ok := b.Body.Attributes["source"]
				if !ok {
					continue
				}
				checked, released, ok := releasedModule(source.Expr)
				if !ok {
					problems = append(problems, fmt.Sprintf("%s calls a module from %s. A site's root fetches every module from the release its site was given - oci://${var.release}//<the module's path>?digest=${var.digest} - or, for a check, reads the same path under ${var.unreleased}. Any other source runs something no site's line chose.", f.Path, tree.Source(t, source.Expr)))
					continue
				}
				if checked != released {
					problems = append(problems, fmt.Sprintf("%s calls %s when checked and %s when released, so the check is of a module no site runs.", f.Path, checked, released))
					continue
				}
				modules = append(modules, path.Clean(released))
			case b.Type == "variable" && len(b.Labels) == 1 && slices.Contains([]string{"release", "digest", "unreleased"}, b.Labels[0]):
				declared[b.Labels[0]] = true
				if d, ok := b.Body.Attributes["default"]; ok && !isLiteral(d.Expr, "") {
					problems = append(problems, fmt.Sprintf("%s gives %q a default of %s. A root that was not told which release to run must run none; a default is a version nobody chose.", f.Path, b.Labels[0], tree.Source(t, d.Expr)))
				}
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
	t.Parallel()
	variable := func(name, def string) string {
		return "variable \"" + name + "\" {\n  type    = string\n  default = " + def + "\n}\n"
	}
	vars := variable("release", `""`) + variable("digest", `""`) + variable("unreleased", `""`)
	call := func(source string) string { return "module \"m\" {\n  source = " + source + "\n}\n" }
	released := call(`var.unreleased != "" ? "${var.unreleased}/parts/thing" : "oci://${var.release}//parts/thing?digest=${var.digest}"`)
	read := func(body string) (*parsed.Tree, []parsed.OpenTofuFile) {
		tree := parsed.Empty(t)
		tree.Write(t, "ground/alpha/calls.tf", body)
		tree.Index(t)
		return tree, tree.OpenTofu(t)
	}
	tree, files := read(vars + released)
	problems, modules := unreleasedModules(t, tree, "ground/alpha", files)
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
		tree, files := read(tc.body)
		problems, _ := unreleasedModules(t, tree, "ground/alpha", files)
		got := strings.Join(problems, "|")
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

func TestUnversionedSitesWantsALineForEverySiteAndNoOther(t *testing.T) {
	t.Parallel()
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
