package repo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every Go module is vetted, built, tested and floored - and none of that is
// remembered.
//
// HOW THIS WENT WRONG, because the shape matters more than the instance.
// scripts/clerk and scripts/inspector were absent from `validate`, from
// `test:go`, and from the coverage gate. Neither was compiled or run by
// anything - not locally, not in CI - for its whole life. Every guard written
// for the clerk had therefore only ever passed in the terminal that wrote it,
// and would have gone on passing after the code it guards stopped working.
//
// Four hand-maintained lists, and not one of them asserted its own
// completeness. A module is created by making a directory with a go.mod in it,
// and nothing about that act suggests any list needs editing. The operator's
// instruction on being shown this was the right one: "we need to dig in and
// harden our guards because you will forget and I will not notice."
//
// So forgetting is now impossible rather than merely detected. taskfile.yml
// discovers the modules, and this refuses any attempt to go back to naming
// them. The one thing still written by hand is each module's coverage floor,
// deliberately: tests/coverage-baseline.json exists to record a decision that a
// floor is worth standing on, and a number nobody chose is not a decision. The
// gate refuses a key it has no baseline for, so a new module fails until
// somebody picks one.

// A go command that names one module under scripts/. Building a specific tool
// in order to run it is fine and common - `task clear-branches` does it - so
// only the verbs that ought to sweep every module are refused.
var namesOneModule = regexp.MustCompile(`go (vet|test) -C scripts/[a-z]`)

func goModules(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(repoRoot(t), "scripts"))
	if err != nil {
		t.Fatalf("reading scripts/: %v", err)
	}
	var modules []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(repoRoot(t), "scripts", e.Name(), "go.mod")); err == nil {
			modules = append(modules, e.Name())
		}
	}
	if len(modules) == 0 {
		t.Fatal("no Go module found under scripts/, so this asserts nothing")
	}
	sort.Strings(modules)
	return modules
}

// The taskfile sweeps every module rather than naming any.
//
// A named module is a list, and a list is the thing that went stale. This does
// not check that the sweep is correct - `task validate` and `task test` do that
// by running - it checks that a sweep is what is there.
func TestTheTaskfileDiscoversModulesRatherThanListingThem(t *testing.T) {
	taskfile := readRepoFile(t, "taskfile.yml")

	var named []string
	for _, line := range strings.Split(taskfile, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue // Prose describing the old shape is not the old shape.
		}
		if namesOneModule.MatchString(line) {
			named = append(named, strings.TrimSpace(line))
		}
	}
	if len(named) > 0 {
		t.Errorf(`taskfile.yml vets or tests named modules:

  %s

That is a list, and a list is what went stale: clerk and inspector were left
off three of them and were never compiled or run by anything. Sweep
scripts/*/go.mod instead, so a module added tomorrow is covered the moment it
exists.`, strings.Join(named, "\n  "))
	}
}

// Every module has a floor somebody chose.
//
// The gate already refuses a key it has no baseline for, so a missing floor
// fails `task test:coverage` - but only when that task runs, and it is not on
// the pull request path. This says the same thing in the tier that is.
func TestEveryGoModuleHasACoverageFloor(t *testing.T) {
	var baseline struct {
		Baselines map[string]float64 `json:"baselines"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, "tests/coverage-baseline.json")), &baseline); err != nil {
		t.Fatalf("parsing tests/coverage-baseline.json: %v", err)
	}
	if len(baseline.Baselines) == 0 {
		t.Fatal("the baseline file declares no floors at all, so this asserts nothing")
	}

	for _, m := range goModules(t) {
		key := "go/" + m
		if _, ok := baseline.Baselines[key]; !ok {
			t.Errorf(`tests/coverage-baseline.json has no floor for %q.

scripts/%s is a Go module, so its coverage is measured and compared - and with
no floor there is nothing to compare against. Six of seven modules were in
exactly that state, including two that no task ran at all.

Add %q with the figure you intend the project to keep standing on. It is
written by hand on purpose: a floor that ratchets automatically records
whatever the last green run happened to hit.`, key, m, key)
		}
	}
}
