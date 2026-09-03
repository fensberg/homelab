package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Every uncovered function in the shipped program is declared, or the build
// fails.
//
// The coverage baseline is one number over a growing program, so it catches a
// collapse and misses drift: adding forty uncovered lines to scripts/contractor
// moves the total by a fraction of a percent. That is not a hypothetical shape
// of failure here - the baseline sat at 24.5 while the real figure was 34.1,
// ten points of slack, for long enough that nobody remembered setting it.
//
// This is the per-function version, kept readable by counting per file. A new
// uncovered function raises its file's count and fails until somebody writes
// the new number down; a newly covered one lowers it and fails the same way,
// so the file tightens as the program gets better tested rather than sagging.
// A file with no entry may have no uncovered functions at all.
//
// It deliberately does not require coverage. Most of this program shells out
// to tofu, ansible, talosctl, kubectl, op and rclone against a real estate,
// and no amount of rule-making changes that. What it requires is that the
// decision has been made and written down, so an untested decision cannot sit
// unnoticed among plumbing that genuinely cannot be tested.

type coverageExemptions struct {
	Files []struct {
		Path      string `yaml:"path"`
		Uncovered int    `yaml:"uncovered"`
		CoveredBy string `yaml:"covered_by"`
	} `yaml:"files"`
}

// The tiers an entry may name. "nothing" is deliberately spellable: a gap
// somebody has looked at and written down is worth more than one nobody has,
// and refusing to allow the honest answer would only produce dishonest ones.
var knownTiers = map[string]bool{
	"e2e": true, "integration": true, "api": true,
	"not-worth-testing": true, "nothing": true,
}

// uncoveredByFile runs the shipped program's own tests with coverage and
// returns how many functions each file reports at 0%.
//
// Measured rather than read from a checked-in profile: a profile committed
// alongside the code it describes is a second copy that drifts, and the whole
// point here is to catch drift.
func uncoveredByFile(t *testing.T) map[string]int {
	t.Helper()
	root := repoRoot(t)
	dir := filepath.Join(root, "scripts", "contractor")

	profile := filepath.Join(t.TempDir(), "cover.out")
	build := exec.Command("go", "test", "-covermode=atomic", "-coverprofile="+profile, "./...")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("running the contractor tests with coverage: %v\n%s", err, out)
	}

	report := exec.Command("go", "tool", "cover", "-func="+profile)
	report.Dir = dir
	out, err := report.Output()
	if err != nil {
		t.Fatalf("reading the coverage profile: %v", err)
	}

	counts := map[string]int{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || f[len(f)-1] != "0.0%" {
			continue
		}
		// "homelab/contractor/internal/run/exec.go:59:  CmdOutput  0.0%"
		path := strings.TrimPrefix(strings.Split(f[0], ":")[0], "homelab/contractor/")
		counts[path]++
	}
	if len(counts) == 0 {
		t.Fatal("the coverage profile reported no uncovered functions at all, which " +
			"is not this program - the parse has stopped matching the report's shape")
	}
	return counts
}

func TestEveryUncoveredFunctionIsDeclared(t *testing.T) {
	root := repoRoot(t)

	body, err := os.ReadFile(filepath.Join(root, "tests", "coverage-exemptions.yml"))
	if err != nil {
		t.Fatalf("reading tests/coverage-exemptions.yml: %v", err)
	}
	var declared coverageExemptions
	if err := yaml.Unmarshal(body, &declared); err != nil {
		t.Fatalf("parsing tests/coverage-exemptions.yml: %v", err)
	}
	if len(declared.Files) == 0 {
		t.Fatal("the exemptions file declares nothing, so this test would pass over " +
			"a program with no coverage at all")
	}

	want := map[string]int{}
	for _, f := range declared.Files {
		if _, dup := want[f.Path]; dup {
			t.Errorf("%s is declared twice; the second entry silently wins", f.Path)
		}
		want[f.Path] = f.Uncovered

		if !knownTiers[f.CoveredBy] {
			t.Errorf(`%s declares covered_by: %q, which is not a tier.

Use e2e, integration or api to name the tier that reaches it;
not-worth-testing for printing and process control; or nothing, which is
allowed and is the point - a gap somebody wrote down beats one nobody did.`,
				f.Path, f.CoveredBy)
		}
	}

	actual := uncoveredByFile(t)

	var paths []string
	for p := range want {
		paths = append(paths, p)
	}
	for p := range actual {
		if _, ok := want[p]; !ok {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)

	for _, p := range paths {
		got, declaredCount := actual[p], want[p]
		switch {
		case got == declaredCount:
			continue

		case declaredCount == 0:
			t.Errorf(`%s has %d uncovered function(s) and no entry in tests/coverage-exemptions.yml.

Either test them, or add an entry saying why they cannot be - naming what the
tested decision is, if one was extracted. An undeclared uncovered function is
the case this file exists to make impossible: something nobody decided about,
sitting among plumbing that was decided about.`, p, got)

		case got > declaredCount:
			t.Errorf(`%s has %d uncovered function(s); tests/coverage-exemptions.yml says %d.

Something was added without a test. Add one, or raise the number here and say
in the commit message why that function cannot be covered.`, p, got, declaredCount)

		default:
			t.Errorf(`%s has %d uncovered function(s); tests/coverage-exemptions.yml says %d.

Coverage improved - lower the number to %d so the file keeps ratcheting. Left
alone it becomes slack, which is exactly how the coverage baseline came to sit
ten points below reality.`, p, got, declaredCount, got)
		}
	}
}
