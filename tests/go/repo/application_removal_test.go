package repo

import (
	"slices"
	"sort"
	"strings"
	"testing"

	"homelab/details/applications"
	"homelab/details/flux"
	"homelab/tests/parsed"
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
	heavy(t, "copies the repository once per application and runs every guard against each copy")
	skipIfInner(t)
	// Beside the other slow guards and not after them: it shares nothing
	// with any of them but the compiled tests, which are made once.
	t.Parallel()

	apps := repository(t).Applications(t)
	bin := testBinary(t)
	for _, a := range apps {
		if by := requiredBy(apps, a.Name); len(by) > 0 {
			t.Logf("%s is required by %s, so it is not removable while they are there", a.Name, strings.Join(by, ", "))
			continue
		}
		t.Run(a.Name, func(t *testing.T) {
			// Two copies, so that nothing the first run leaves behind in its
			// tree is taken for part of the repository by the second.
			untouched := scratchRepo(t)
			described := map[string]bool{}
			for _, problem := range ledgerNoLongerDescribes(t, untouched) {
				described[problem] = true
			}
			// Every guard against the copy as it is, and then against the
			// copy without the application: one after the other. They ran
			// side by side, which put two whole suites - each with its
			// coverage build and its Semgrep scans - on one runner beside the
			// lane's own slow guards, and a Rego test that takes milliseconds
			// was held past the five seconds conftest verify gives each test
			// (#781). One suite at a time is this guard's share of the lane.
			before, beforeOutput := allVerdicts(bin, untouched)
			if len(before) == 0 {
				t.Fatalf("no test verdict was parsed from the run against the copy as it is:\n%s", beforeOutput)
			}
			scratch := scratchRepo(t)
			removeApplication(t, scratch, a)
			indexScratchTree(t, scratch)

			if slices.ContainsFunc(scratch.Applications(t), func(o applications.Application) bool { return o.Name == a.Name }) {
				t.Fatalf("%s was not removed from the copy, so this proved nothing", a.Name)
			}
			// The ledger's runner does not run inside a copy, so what it
			// would say is asked directly: every proof in tests/mutations.yml
			// still names a file that is there, and text that is in it.
			for _, problem := range ledgerNoLongerDescribes(t, scratch) {
				// Already so with the application there: the ledger's own
				// runner reports that, and it is not this removal's doing.
				if described[problem] {
					continue
				}
				t.Errorf("with %s removed, %s\n\nA proof about the estate breaks a file of the estate's, or plants one; a proof that needs an application's file belongs in that application's own ledger.", a.Name, problem)
			}
			after, afterOutput := allVerdicts(bin, scratch)
			if len(after) == 0 {
				t.Fatalf("no test verdict was parsed from the run against the copy without %s:\n%s", a.Name, afterOutput)
			}
			var broke []string
			for name, passed := range after {
				if !passed && before[name] {
					broke = append(broke, name)
				}
			}
			sort.Strings(broke)
			// What the test said in the run that failed it, and then alone in
			// the same copy. Only the second was shown, and on #779 it was a
			// pass: the failure had been said in a run whose output was thrown
			// away, so the report named a broken test and showed it passing.
			for _, name := range broke {
				_, alone := runGuard(t, bin, scratch, strings.SplitN(name, "/", 2)[0])
				t.Errorf("%s passes with %s in the repository and fails with only its directory and its sites' blocks removed.\n\n"+
					"Something outside %s depends on it. Move that into the application's directory, or make it read what "+
					"applications declare instead of knowing this one.\n\nBeside every other test, it said:\n\n%s\n\nRun alone in the same copy, it said:\n\n%s",
					name, a.Name, a.Root, outputOf(afterOutput, name), alone)
			}
		})
	}
}

// removeApplication removes an application from a copy of the repository the
// way a person would: its directory, and its block in every site's file.
func removeApplication(t *testing.T, scratch *parsed.Tree, a applications.Application) {
	t.Helper()
	scratch.Remove(t, a.Root)
	for _, rel := range scratch.Files(t) {
		if parts := strings.Split(rel, "/"); len(parts) == 3 && parts[0] == applications.SitesDir && parts[2] == applications.SiteFile {
			scratch.RemoveDocuments(t, rel, blockOf(a))
		}
	}
}

// ledgerNoLongerDescribes is every entry of the estate's ledger that could
// not be run against a tree: its file is gone, or what it breaks in that file
// is not there exactly once.
func ledgerNoLongerDescribes(t *testing.T, tree *parsed.Tree) []string {
	t.Helper()
	var out []string
	for _, m := range readLedger(t).Mutations {
		if m.Creates() {
			continue
		}
		switch n := tree.Places(t, m); {
		case n < 0:
			out = append(out, "the ledger's proof of "+m.Guard+" breaks "+m.File+", which is no longer there.")
		case n != 1:
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

// blockOf picks a site's documents that are one application's block: the
// release source named for it, and the Kustomization that runs its
// directory.
func blockOf(a applications.Application) func(doc any) bool {
	return func(doc any) bool {
		m, _ := doc.(map[string]any)
		kind, _ := m["kind"].(string)
		meta, _ := m["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		spec, _ := m["spec"].(map[string]any)
		path, _ := spec["path"].(string)
		source := kind == flux.OCIRepository && name == a.Name
		runs := kind == flux.Kustomization && strings.HasPrefix(path, "./"+a.Root+"/")
		return source || runs
	}
}

// An application's block is its release source and the Kustomization that
// runs its directory, and nothing else: not another application's, and not
// one whose name only begins with its own.
func TestABlockIsAnApplicationsOwnSourceAndWhatRunsIt(t *testing.T) {
	light(t)
	app := applications.Application{Name: "first", Root: applications.Dir + "/first"}
	doc := func(kind, name, path string) any {
		return map[string]any{"kind": kind, "metadata": map[string]any{"name": name}, "spec": map[string]any{"path": path}}
	}
	for _, c := range []struct {
		doc  any
		want bool
	}{
		{doc(flux.OCIRepository, "first", ""), true},
		{doc(flux.Kustomization, "workloads-production", "./"+applications.Dir+"/first/production"), true},
		{doc(flux.OCIRepository, "second", ""), false},
		{doc(flux.Kustomization, "x", "./"+applications.Dir+"/first-and-more/production"), false},
		{doc("ConfigMap", "first", ""), false},
		{"not a mapping", false},
	} {
		if got := blockOf(app)(c.doc); got != c.want {
			t.Errorf("blockOf(first)(%v) = %v, want %v", c.doc, got, c.want)
		}
	}
	apps := []applications.Application{{Name: "a", Requires: []string{"b"}}, {Name: "b"}, {Name: "c", Requires: []string{"b"}}}
	if got := strings.Join(requiredBy(apps, "b"), ","); got != "a,c" || len(requiredBy(apps, "a")) != 0 {
		t.Errorf("what requires b was read as %q", got)
	}
}
