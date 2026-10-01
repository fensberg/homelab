package repo

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"homelab/contractor/config"
	"homelab/contractor/pin"
	"homelab/details/tofufiles"
)

// A site runs its modules at its pin, and no other way.
//
// WHY THIS EXISTS. A site's roots build nothing; they call modules. Called by
// path, every site runs the modules as they stand on the commit being
// converged, so a change reaches every site on the merge that made it and
// there is no way to try it on one first. A pin is the line that says which
// commit of the modules a site runs (management/pins.json), and the
// contractor places that commit's tree where the roots read from.
//
// The pin only holds if a root cannot reach its modules around it. So every
// module a site's root calls is read from the pinned tree and from nowhere
// else, the root has no default tree to fall back on, and the pins
// themselves are commits rather than names that move.
func TestEverySiteRootRunsItsModulesAtItsPin(t *testing.T) {
	sources := tofuSources(t)
	for _, name := range config.Roots {
		root := "management/" + name
		problems, modules := unpinnedModules(root, tofufiles.In(sources, root))
		for _, p := range problems {
			t.Error(p)
		}
		if len(modules) == 0 {
			t.Errorf("%s calls no module, so either it builds something itself or this has stopped reading it", root)
		}
		// What the root calls is there to call, in the working tree: the
		// checks read it from there, and the next pin will.
		for _, dir := range modules {
			if len(tofufiles.In(sources, dir)) == 0 {
				t.Errorf("%s calls a module at %s, and no OpenTofu is tracked there", root, dir)
			}
		}
	}
	if _, err := pin.Read(repoRoot(t)); err != nil {
		t.Error(err)
	}
}

var (
	moduleSourceLine = regexp.MustCompile(`(?m)^\s*source\s*=\s*"([^"]*)"`)
	treeVariable     = regexp.MustCompile(`(?s)variable\s+"tree"\s*\{(.*?)\n\}`)
	// The one form a site root's module source takes: up to the top of the
	// repository, into the tree it was told to read, then the module's path.
	pinnedSource = regexp.MustCompile(`^\.\./\.\./\.pinned/\$\{var\.tree\}/(.+)$`)
)

// unpinnedModules is each way one root reaches its modules around the pin,
// and the repository-relative directory of each module it calls properly.
func unpinnedModules(root string, files map[string]string) (problems, modules []string) {
	declaresTree := false
	for rel, body := range files {
		code := tofufiles.Code(body)
		for _, m := range moduleSourceLine.FindAllStringSubmatch(code, -1) {
			pinned := pinnedSource.FindStringSubmatch(m[1])
			if pinned == nil {
				problems = append(problems, fmt.Sprintf("%s calls a module from %q. A site's root reads every module from its pinned tree, \"../../.pinned/${var.tree}/<the module's path>\", so the site runs the commit it is pinned to. Any other source runs whatever is on the commit being converged, on every site at once.", rel, m[1]))
				continue
			}
			modules = append(modules, filepath.ToSlash(filepath.Clean(pinned[1])))
		}
		if m := treeVariable.FindStringSubmatch(code); m != nil {
			declaresTree = true
			if regexp.MustCompile(`(?m)^\s*default\s*=`).MatchString(m[1]) {
				problems = append(problems, fmt.Sprintf("%s gives the tree a default. A root that was not told which version of its modules to run must run none; a default is a version nobody chose.", rel))
			}
		}
	}
	if !declaresTree {
		problems = append(problems, root+" declares no `tree` variable, so nothing tells it which pinned tree to read its modules from.")
	}
	sort.Strings(problems)
	sort.Strings(modules)
	return problems, modules
}

func TestUnpinnedModulesRefusesEveryWayAroundThePin(t *testing.T) {
	const tree = "variable \"tree\" {\n  type = string\n}\n"
	pinned := "module \"m\" {\n  source = \"../../.pinned/${var.tree}/parts/thing\"\n}\n"
	problems, modules := unpinnedModules("ground/alpha", map[string]string{"ground/alpha/calls.tf": tree + pinned})
	if len(problems) != 0 || strings.Join(modules, " ") != "parts/thing" {
		t.Fatalf("a root reading its module from its pin: %v, %v", problems, modules)
	}
	for name, tc := range map[string]struct{ body, want string }{
		"by path":                {tree + "module \"m\" {\n  source = \"../../parts/thing\"\n}\n", "calls a module from"},
		"from a registry":        {tree + "module \"m\" {\n  source = \"example/thing/x\"\n}\n", "calls a module from"},
		"from git":               {tree + "module \"m\" {\n  source = \"git::https://example.invalid/r.git//parts/thing?ref=main\"\n}\n", "calls a module from"},
		"a fixed tree":           {tree + "module \"m\" {\n  source = \"../../.pinned/site0/parts/thing\"\n}\n", "calls a module from"},
		"a default tree":         {"variable \"tree\" {\n  type    = string\n  default = \"worktree\"\n}\n" + pinned, "gives the tree a default"},
		"no tree variable":       {pinned, "declares no `tree` variable"},
		"a commented-out source": {tree + pinned + "#   source = \"../../parts/thing\"\n", ""},
	} {
		problems, _ := unpinnedModules("ground/alpha", map[string]string{"ground/alpha/calls.tf": tc.body})
		got := strings.Join(problems, "|")
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}
