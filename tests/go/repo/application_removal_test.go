package repo

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"homelab/details/applications"
	"homelab/details/flux"
)

// Removing an application is deleting its directory and its block in each
// site that runs it - and this does exactly that, to each application in
// turn, and requires every guard to still pass.
//
// That is the estate's test of whether an application is modular (#590): what
// removing it costs. The first application failed it by a wide margin. Its
// name was in thirty-five files outside its own directory - a workflow, three
// programs, the platform module, the vault template, a routes file, the pins,
// seven guards - and each of those was a place the next application would
// have had to be written into as well.
//
// Nothing here knows what an application's name looks like or where it might
// hide. It removes the application the way a person would and asks every
// guard in this package what broke: a list that still names it, a ledger
// entry that breaks one of its files, a guard that needs it to exist, a
// program that reads a file it had. So it finds a coupling whatever shape it
// takes, including the ones nobody thought to forbid.
//
// An application another requires is not removable while that other is
// there, and the declarations refuse it by name
// (homelab/details/applications): it is passed over here, and its turn comes
// when what requires it is gone.
func TestAnApplicationIsRemovedByDeletingItsDirectoryAndItsBlocks(t *testing.T) {
	skipIfInner(t)
	heavy(t, "copies the repository once per application and runs every guard against each copy")

	root := repoRoot(t)
	apps, err := applications.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	bin := testBinary(t)
	for _, a := range apps {
		if by := requiredBy(apps, a.Name); len(by) > 0 {
			t.Logf("%s is required by %s, so it is not removable while they are there", a.Name, strings.Join(by, ", "))
			continue
		}
		t.Run(a.Name, func(t *testing.T) {
			// Two copies, so that nothing the first run leaves behind in its
			// tree is taken for part of the repository by the second.
			before := runAll(t, bin, scratchRepo(t))
			scratch := scratchRepo(t)
			removeApplication(t, scratch, a)
			indexScratchTree(t, scratch)

			if left, err := applications.Read(scratch); err != nil || slices.ContainsFunc(left, func(o applications.Application) bool { return o.Name == a.Name }) {
				t.Fatalf("%s was not removed from the copy (%v), so this proved nothing", a.Name, err)
			}
			// The ledger's runner does not run inside a copy, so what it
			// would say is asked directly: every proof in tests/mutations.yml
			// still names a file that is there, and text that is in it.
			for _, problem := range ledgerNoLongerDescribes(t, scratch) {
				t.Errorf("with %s removed, %s\n\nA proof about the estate breaks a file of the estate's, or plants one; a proof that needs an application's file belongs in that application's own ledger.", a.Name, problem)
			}
			after := runAll(t, bin, scratch)
			var broke []string
			for name, passed := range after {
				if !passed && before[name] {
					broke = append(broke, name)
				}
			}
			sort.Strings(broke)
			for _, name := range broke {
				_, out := runGuard(t, bin, scratch, strings.SplitN(name, "/", 2)[0])
				t.Errorf("%s passes with %s in the repository and fails with only its directory and its sites' blocks removed.\n\n"+
					"Something outside %s depends on it. Move that into the application's directory, or make it read what "+
					"applications declare instead of knowing this one.\n\n%s", name, a.Name, a.Root, out)
			}
		})
	}
}

// removeApplication removes an application from a copy of the repository the
// way a person would: its directory, and its block in every site's file.
func removeApplication(t *testing.T, root string, a applications.Application) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(a.Root))); err != nil {
		t.Fatal(err)
	}
	sites, err := filepath.Glob(filepath.Join(root, applications.SitesDir, "*", applications.SiteFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, site := range sites {
		body, err := os.ReadFile(site)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(site, []byte(withoutApplication(string(body), a)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// ledgerNoLongerDescribes is every entry of the estate's ledger that could
// not be run against the tree at root: its file is gone, or what it breaks
// in that file is not there exactly once.
func ledgerNoLongerDescribes(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, m := range readLedger(t).Mutations {
		if m.creates() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(m.File)))
		switch {
		case err != nil:
			out = append(out, "the ledger's proof of "+m.Guard+" breaks "+m.File+", which is no longer there.")
		case strings.Count(string(body), m.Find) != 1:
			out = append(out, "the ledger's proof of "+m.Guard+" breaks text in "+m.File+" that is no longer there exactly once.")
		}
	}
	return out
}

// requiredBy is the applications that require the named one.
func requiredBy(apps []applications.Application, name string) []string {
	var out []string
	for _, a := range apps {
		if slices.Contains(a.Requires, name) {
			out = append(out, a.Name)
		}
	}
	return out
}

// withoutApplication is a site's applications file with one application's
// block taken out: the release source named for it, and the Kustomization
// that reconciles a directory of its own. Everything else is left exactly as
// it was written.
func withoutApplication(body string, a applications.Application) string {
	const separator = "\n---\n"
	lead := ""
	if strings.HasPrefix(body, "---\n") {
		lead, body = "---\n", strings.TrimPrefix(body, "---\n")
	}
	var kept []string
	for _, doc := range strings.Split(body, separator) {
		var parsed struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Spec struct {
				Path string `yaml:"path"`
			} `yaml:"spec"`
		}
		// A document that does not parse is not this application's block,
		// and is left for the guards to judge.
		_ = yaml.Unmarshal([]byte(doc), &parsed)
		source := parsed.Kind == flux.OCIRepository && parsed.Metadata.Name == a.Name
		runs := parsed.Kind == flux.Kustomization && strings.HasPrefix(parsed.Spec.Path, "./"+a.Root+"/")
		if !source && !runs {
			kept = append(kept, doc)
		}
	}
	return lead + strings.Join(kept, separator)
}

// The removal is held to what it claims, against a file written here: one
// application's two documents go, and another's stay as written - comments,
// order and all.
func TestWithoutApplicationTakesOutOneBlockAndLeavesTheRest(t *testing.T) {
	app := func(name string) applications.Application {
		return applications.Application{Name: name, Root: applications.Dir + "/" + name}
	}
	block := func(name, kustomization string) string {
		return "# " + name + "'s release\nkind: OCIRepository\nmetadata:\n  name: " + name + "\n---\nkind: Kustomization\nmetadata:\n  name: " + kustomization + "\nspec:\n  path: ./" + applications.Dir + "/" + name + "/production\n"
	}
	first, second := block("first", "workloads-production"), block("second", "second")
	both := "---\n" + first + "---\n" + second

	if got := withoutApplication(both, app("first")); got != "---\n"+second {
		t.Errorf("removing the first application left:\n%s", got)
	}
	if got := withoutApplication(both, app("second")); got != "---\n"+strings.TrimSuffix(first, "\n") {
		t.Errorf("removing the second application left:\n%s", got)
	}
	if got := withoutApplication(both, app("absent")); got != both {
		t.Errorf("removing an application the site does not run changed the file:\n%s", got)
	}
	// One whose name only begins another's is a different application.
	if got := withoutApplication("---\n"+block("first-and-more", "x"), app("first")); got != "---\n"+block("first-and-more", "x") {
		t.Errorf("removing an application took out one whose name begins with its name:\n%s", got)
	}
	apps := []applications.Application{{Name: "a", Requires: []string{"b"}}, {Name: "b"}, {Name: "c", Requires: []string{"b"}}}
	if got := strings.Join(requiredBy(apps, "b"), ","); got != "a,c" || len(requiredBy(apps, "a")) != 0 {
		t.Errorf("what requires b was read as %q", got)
	}
}
