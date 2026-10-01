package repo

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"homelab/contractor/config"
)

// An application is named only where it lives.
//
// WHY THIS EXISTS. The estate's test of whether an application is modular is
// what removing it costs: its directory, and its block in each site that runs
// it (#590). When this was written the one application there is was named in
// thirty-five files outside its own - a workflow, two programs, a shared
// module, the config template, seven guards, the pins - so removing it meant
// knowing all thirty-five.
//
// So an application's name may appear in its own directory, in the directory
// of a site that was given it (that is the assignment), in the directory of
// an application that declares it requires this one (it has to address it),
// and in prose. Anywhere else is a mechanism that knows an application by
// name, and should read what applications declare instead.
//
// THE DEBT. tests/application-debt.yml lists every file that names one
// today, with how many lines do. The count is exact, in both directions: a
// new mention fails until somebody writes it down, and a removed one fails
// until the number comes down. The file is done when it is empty.
//
// WHAT THIS CANNOT SEE. A thing an application owns under another name - the
// Service a route points at, a supplier only it uses. The removal test is
// what finds those: delete the directory, and whatever still expects it
// fails.
const applicationDebtFile = "tests/application-debt.yml"

type applicationDebt struct {
	NamedOutside map[string]int `yaml:"named_outside"`
}

func TestAnApplicationIsNamedOnlyWhereItLives(t *testing.T) {
	root := repoRoot(t)
	apps, err := config.Applications(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) == 0 {
		t.Fatal("no application was found, so no name was looked for and this checked nothing")
	}
	var debt applicationDebt
	if err := yaml.Unmarshal([]byte(readRepoFile(t, applicationDebtFile)), &debt); err != nil {
		t.Fatalf("%s: %v", applicationDebtFile, err)
	}

	files := map[string]string{}
	for _, rel := range tracked(t, func(rel string) bool { return rel != applicationDebtFile }) {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		// Not text: an image, a font. Nothing in one names anything.
		if bytes.IndexByte(body, 0) >= 0 {
			continue
		}
		files[rel] = string(body)
	}
	found := namedOutside(apps, declaredSites(t), files)
	for _, p := range debtProblems(found, debt.NamedOutside) {
		t.Error(p)
	}
}

// namedOutside is, for every file that names an application somewhere it
// does not live, how many lines of it do - one more if its path does.
func namedOutside(apps []config.Application, sites []string, files map[string]string) map[string]int {
	out := map[string]int{}
	for _, a := range apps {
		name := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(a.Name))
		for rel, body := range files {
			if livesIn(rel, a, apps, sites) {
				continue
			}
			n := 0
			if name.MatchString(rel) {
				n++
			}
			for _, line := range strings.Split(body, "\n") {
				if name.MatchString(line) {
					n++
				}
			}
			if n > 0 {
				out[rel] += n
			}
		}
	}
	return out
}

// livesIn reports whether a file is somewhere an application may be named.
func livesIn(rel string, a config.Application, apps []config.Application, sites []string) bool {
	// Its own directory, and - until its per-environment settings move in
	// beside the rest of it - its overlay in each environment.
	if strings.HasPrefix(rel, config.ApplicationsDir+"/"+a.Name+"/") {
		return true
	}
	if parts := strings.Split(rel, "/"); len(parts) > 4 && parts[0] == "environments" && parts[2] == "applications" && parts[3] == a.Name {
		return true
	}
	// A site that was given it: naming it there is the assignment.
	for _, site := range sites {
		if strings.HasPrefix(rel, fluxTree+"/"+site+"/") {
			return true
		}
	}
	// An application that declares it requires this one has to address it.
	for _, other := range apps {
		if other.Name == a.Name || !strings.HasPrefix(rel, config.ApplicationsDir+"/"+other.Name+"/") {
			continue
		}
		for _, r := range other.Requires {
			if r == a.Name {
				return true
			}
		}
	}
	// Prose explains, and the records are history.
	return strings.HasPrefix(rel, "docs/") || strings.HasSuffix(rel, ".md")
}

// debtProblems holds what was found to what is declared, exactly.
func debtProblems(found, declared map[string]int) []string {
	var out []string
	for rel, n := range found {
		switch d, ok := declared[rel]; {
		case !ok:
			out = append(out, fmt.Sprintf("%s names an application on %d line(s), outside where it lives. Read what applications declare (config.Applications) instead of naming one, or put this in the application's own directory. If it cannot move yet, add it to %s.", rel, n, applicationDebtFile))
		case n > d:
			out = append(out, fmt.Sprintf("%s names an application on %d line(s), and %s allows %d. The debt may only shrink: take the new mention out.", rel, n, applicationDebtFile, d))
		case n < d:
			out = append(out, fmt.Sprintf("%s names an application on %d line(s), down from %d. Lower the number in %s, so it cannot creep back.", rel, n, d, applicationDebtFile))
		}
	}
	for rel := range declared {
		if _, still := found[rel]; !still {
			out = append(out, fmt.Sprintf("%s lists %s, which no longer names an application. Remove the line: the debt is paid.", applicationDebtFile, rel))
		}
	}
	sort.Strings(out)
	return out
}

func TestNamedOutsideAllowsAnApplicationsNameOnlyWhereItLives(t *testing.T) {
	apps := []config.Application{{Name: "alpha"}, {Name: "beta", Requires: []string{"alpha"}}, {Name: "gamma"}}
	dir := config.ApplicationsDir
	files := map[string]string{
		dir + "/alpha/base/thing.yaml":                     "name: alpha\n",
		"environments/prod/applications/alpha/values.yaml": "name: alpha\n",
		fluxTree + "/north/work.yaml":                      "runs: alpha\nand: gamma\n",
		dir + "/beta/base/thing.yaml":                      "upstream: alpha.alpha.svc\n",
		"docs/notes.md":                                    "alpha was first\n",
		"README.md":                                        "alpha\n",
		// Each of these is a mechanism that knows an application by name.
		"shared/module.tf":                         "resource \"x\" \"alpha\" {}\nname = \"ALPHA_KEY\"\nunrelated = 1\n",
		"shared/alpha_test.go":                     "package x\n",
		dir + "/gamma/base/thing.yaml":             "calls: alpha\n",
		fluxTree + "/core/shared.yaml":             "name: beta\n",
		"environments/prod/applications/list.yaml": "- alpha\n",
		"shared/quiet.txt":                         "nothing here\n",
	}
	got := namedOutside(apps, []string{"north"}, files)
	want := map[string]int{
		"shared/module.tf":                         2,
		"shared/alpha_test.go":                     1,
		dir + "/gamma/base/thing.yaml":             1,
		fluxTree + "/core/shared.yaml":             1,
		"environments/prod/applications/list.yaml": 1,
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}

	for name, tc := range map[string]struct {
		found, declared map[string]int
		want            string
	}{
		"as declared":   {map[string]int{"a": 2}, map[string]int{"a": 2}, ""},
		"a new file":    {map[string]int{"a": 2, "b": 1}, map[string]int{"a": 2}, "b names an application on 1 line(s), outside where it lives"},
		"a new mention": {map[string]int{"a": 3}, map[string]int{"a": 2}, "allows 2"},
		"one fewer":     {map[string]int{"a": 1}, map[string]int{"a": 2}, "down from 2"},
		"a debt paid":   {map[string]int{}, map[string]int{"a": 2}, "the debt is paid"},
	} {
		got := strings.Join(debtProblems(tc.found, tc.declared), "|")
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}
