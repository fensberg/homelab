package repo

import (
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

// goModules is the name of every module under scripts/, derived from
// goModuleDirs rather than read from the filesystem a second way. It read
// scripts/ directly until the building-block guards needed every module in the
// repository, and two enumerations of one set would have been the first thing
// those guards refused.
func goModules(t *testing.T) []string {
	t.Helper()
	var modules []string
	for _, dir := range goModuleDirs(t) {
		if name, ok := strings.CutPrefix(dir, "scripts/"); ok && !strings.Contains(name, "/") {
			modules = append(modules, name)
		}
	}
	if len(modules) == 0 {
		t.Fatal("no Go module found under scripts/, so this asserts nothing")
	}
	return modules
}

// Every module has a floor somebody chose.
//
// The gate already refuses a key it has no baseline for, so a missing floor
// fails `task test:coverage` - but only when that task runs, and it is not on
// the pull request path. This says the same thing in the tier that is.
func TestEveryGoModuleHasACoverageFloor(t *testing.T) {
	t.Parallel()
	var baseline struct {
		Baselines map[string]float64 `json:"baselines"`
	}
	repository(t).JSONInto(t, "tests/coverage-baseline.json", &baseline)
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
