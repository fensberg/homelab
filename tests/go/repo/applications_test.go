package repo

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"homelab/details/applications"
)

// An application is named only where it lives.
//
// WHY THIS EXISTS. The estate's test of whether an application is modular is
// what removing it costs: its directory, and its block in each site that runs
// it (#590). When this was written the one application there was had its name
// in thirty-five files outside its own - a workflow, three programs, a shared
// module, the config template, seven guards, the pins - so removing it meant
// knowing all thirty-five.
//
// So an application's name may appear in its own directory, in the
// applications file of a site (that is the assignment), in the directory of
// an application that declares it requires this one (it has to address it),
// and in prose. Anywhere else is a mechanism that knows an application by
// name, and should read what applications declare instead. Nothing is
// exempted and there is no list of what has not moved yet: there is nothing
// that has not.
//
// WHAT THIS IS NOT. It matches a name, so it is the cheap half and it answers
// at push. A thing an application owns under another name - a Service a
// route points at, a guard that needs the application to exist - it cannot
// see. What finds those is doing the removal:
// TestAnApplicationIsRemovedByDeletingItsDirectoryAndItsBlocks deletes each
// application and runs every guard. And what stops an application reaching
// outside itself in the first place is not a guard at all: its release holds
// its own directory and nothing else, it is reconciled as an identity that
// may only manage its own namespace, and its secrets are made from its own
// vault item (docs/epochs/02-abstraction.md).
func TestAnApplicationIsNamedOnlyWhereItLives(t *testing.T) {
	heavy(t, "semgrep reads every file for the applications' names, a few seconds")
	tree := repository(t)
	apps := tree.Applications(t)
	files := tree.Files(t)
	const atLeastAHundredFiles = 100
	if len(files) < atLeastAHundredFiles {
		t.Fatalf("only %d tracked files were listed, so nothing was read for the applications' names", len(files))
	}
	// Every line of every file that names an application, found by Semgrep
	// from a rule this builds out of what the applications declare: the
	// names are the estate's, so no rule written down could hold them.
	var rules []map[string]any
	for _, a := range apps {
		rules = append(rules, map[string]any{
			"id": "names-" + a.Name, "languages": []string{"generic"}, "severity": "INFO",
			"message": a.Name, "metadata": map[string]any{"about": a.Name},
			"pattern-regex": `(?i)` + regexp.QuoteMeta(a.Name),
		})
	}
	named := map[string]map[string]map[int]bool{}
	for _, f := range scanText(t, tree, rules, files) {
		if named[f.About] == nil {
			named[f.About] = map[string]map[int]bool{}
		}
		if named[f.About][f.Path] == nil {
			named[f.About][f.Path] = map[int]bool{}
		}
		named[f.About][f.Path][f.Line] = true
	}
	// No floor on how many applications there are: an estate with none
	// names none. What this finds is proved against names placed here, in
	// TestNamedOutsideAllowsAnApplicationsNameOnlyWhereItLives.
	for _, problem := range namedOutside(apps, files, named) {
		t.Error(problem)
	}
}

// namedOutside is every file that names an application somewhere it does not
// live, with how many lines of it do - one more if its path does. named is the
// lines naming each application, by its name and the file.
func namedOutside(apps []applications.Application, files []string, named map[string]map[string]map[int]bool) []string {
	var out []string
	for _, a := range apps {
		name := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(a.Name))
		for _, rel := range files {
			if livesIn(rel, a, apps) {
				continue
			}
			n := len(named[a.Name][rel])
			if name.MatchString(rel) {
				n++
			}
			if n > 0 {
				out = append(out, fmt.Sprintf("%s names the application %s on %d line(s), outside where it lives. Read what applications declare (homelab/details/applications) instead of naming one, or put this in %s.", rel, a.Name, n, a.Root))
			}
		}
	}
	sort.Strings(out)
	return out
}

// livesIn reports whether a file is somewhere an application may be named.
func livesIn(rel string, a applications.Application, apps []applications.Application) bool {
	// Its own directory.
	if strings.HasPrefix(rel, a.Root+"/") {
		return true
	}
	// A site's applications file: naming it there is the assignment.
	if parts := strings.Split(rel, "/"); len(parts) == 3 && parts[0] == applications.SitesDir && parts[2] == applications.SiteFile {
		return true
	}
	// An application that declares it requires this one has to address it.
	for _, other := range apps {
		if other.Name != a.Name && strings.HasPrefix(rel, other.Root+"/") && slices.Contains(other.Requires, a.Name) {
			return true
		}
	}
	// Prose explains, and the records are history.
	return strings.HasPrefix(rel, "docs/") || strings.HasSuffix(rel, ".md")
}

func TestNamedOutsideAllowsAnApplicationsNameOnlyWhereItLives(t *testing.T) {
	app := func(name string, requires ...string) applications.Application {
		return applications.Application{Name: name, Root: applications.Dir + "/" + name, Requires: requires}
	}
	apps := []applications.Application{app("alpha"), app("beta", "alpha"), app("gamma")}
	dir := applications.Dir
	// Each file, and the lines of it naming each application, as a scan
	// would find them.
	lines := func(n int) map[int]bool {
		out := map[int]bool{}
		for i := 1; i <= n; i++ {
			out[i] = true
		}
		return out
	}
	named := map[string]map[string]map[int]bool{
		"alpha": {
			dir + "/alpha/base/thing.yaml":     lines(1),
			applications.SiteFilePath("north"): lines(1),
			dir + "/beta/base/thing.yaml":      lines(1),
			"docs/notes.md":                    lines(1),
			"README.md":                        lines(1),
			// Each of these is a mechanism that knows an application by name.
			"shared/module.tf":             lines(2),
			dir + "/gamma/base/thing.yaml": lines(1),
		},
		"beta":  {applications.SitesDir + "/core/shared.yaml": lines(1)},
		"gamma": {applications.SiteFilePath("north"): lines(1), applications.SitesDir + "/north/elsewhere.yaml": lines(1), applications.SitesDir + "/north/deep/" + applications.SiteFile: lines(1)},
	}
	files := []string{
		dir + "/alpha/base/thing.yaml", applications.SiteFilePath("north"), dir + "/beta/base/thing.yaml",
		"docs/notes.md", "README.md", "shared/quiet.txt", "shared/module.tf", "shared/alpha_test.go",
		dir + "/gamma/base/thing.yaml", applications.SitesDir + "/core/shared.yaml",
		applications.SitesDir + "/north/elsewhere.yaml", applications.SitesDir + "/north/deep/" + applications.SiteFile,
	}
	got := namedOutside(apps, files, named)
	want := []string{
		applications.SitesDir + "/core/shared.yaml names the application beta on 1 line(s)",
		applications.SitesDir + "/north/deep/" + applications.SiteFile + " names the application gamma on 1 line(s)",
		applications.SitesDir + "/north/elsewhere.yaml names the application gamma on 1 line(s)",
		dir + "/gamma/base/thing.yaml names the application alpha on 1 line(s)",
		"shared/alpha_test.go names the application alpha on 1 line(s)",
		"shared/module.tf names the application alpha on 2 line(s)",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d problem(s), want %d:\n  %s", len(got), len(want), strings.Join(got, "\n  "))
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("problem %d is\n  %s\nwant it to begin\n  %s", i, got[i], want[i])
		}
	}
	if got := namedOutside(nil, files, named); len(got) != 0 {
		t.Errorf("an estate with no application named one: %v", got)
	}
}
