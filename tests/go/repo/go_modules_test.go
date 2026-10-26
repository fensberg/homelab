package repo

import (
	"sort"
	"strings"
	"testing"

	"homelab/tests/parsed"
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
	light(t)
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

// Every floor is one a lane measures. TestEveryGoModuleHasACoverageFloor
// holds every module to having a floor; this is the other direction. The
// helpers' floor sat in tests/coverage-baseline.json while no lane measured
// it, so it held them to nothing in CI, and its absence looked like health
// (#792). The floors a lane measures are the keys its steps hand
// scripts/coverage-gate.sh, read through the shell parser, with go/$module
// worked out from the modules the tree has.
func TestEveryCoverageFloorIsOneALaneMeasures(t *testing.T) {
	light(t)
	var baseline struct {
		Baselines map[string]float64 `json:"baselines"`
	}
	repository(t).JSONInto(t, "tests/coverage-baseline.json", &baseline)
	if len(baseline.Baselines) == 0 {
		t.Fatal("the baseline file declares no floors at all, so this asserts nothing")
	}
	measured := gatedFloors(workflowCommands(t), goModules(t))
	if len(measured) == 0 {
		t.Fatal("no workflow step runs scripts/coverage-gate.sh, so no floor is measured anywhere")
	}
	var unmeasured []string
	for key := range baseline.Baselines {
		if !measured[key] {
			unmeasured = append(unmeasured, key)
		}
	}
	sort.Strings(unmeasured)
	for _, key := range unmeasured {
		t.Errorf("tests/coverage-baseline.json holds a floor for %q, and no workflow step hands it to scripts/coverage-gate.sh. A floor nothing measures holds what it names to nothing, and looks like health.", key)
	}
}

// gatedFloors is every floor key the commands hand the coverage gate, with a
// key written for every module (go/$module) worked out for each one.
func gatedFloors(commands []parsed.Command, modules []string) map[string]bool {
	out := map[string]bool{}
	for _, c := range commands {
		if len(c.Words) < 3 || c.Words[0] != "bash" || c.Words[1] != "scripts/coverage-gate.sh" {
			continue
		}
		key := strings.Trim(c.Words[2], `"'`)
		if !strings.Contains(key, "$module") && !strings.Contains(key, "${module}") {
			out[key] = true
			continue
		}
		for _, m := range modules {
			out[strings.NewReplacer("${module}", m, "$module", m).Replace(key)] = true
		}
	}
	return out
}

// The reading, on commands made up here.
func TestAFloorIsMeasuredWhereTheGateIsHandedIt(t *testing.T) {
	light(t)
	got := gatedFloors([]parsed.Command{
		{Words: []string{"bash", "scripts/coverage-gate.sh", `"go/$module"`, `"$pct"`}},
		{Words: []string{"bash", "scripts/coverage-gate.sh", "go/tests-helpers", "$pct"}},
		{Words: []string{"bash", "scripts/other.sh", "js", "$pct"}},
		{Words: []string{"bash", "scripts/coverage-gate.sh"}},
	}, []string{"contractor", "security"})
	for _, want := range []string{"go/contractor", "go/security", "go/tests-helpers"} {
		if !got[want] {
			t.Errorf("%s was not read as measured: %v", want, got)
		}
	}
	if got["js"] || len(got) != 3 {
		t.Errorf("read %v; want the three the gate is handed and nothing else", got)
	}
}
