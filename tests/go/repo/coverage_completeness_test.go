package repo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Coverage is discovered and fails closed.
//
// WHY THIS EXISTS, in the operator's words: "I do not trust myself to be a good
// reviewer and I do not trust you to be a good coder. We therefore must build
// safeguards in code to protect both parties from doing something stupid."
//
// The regime this replaces was an allow list. `task test:coverage` measured one
// Go module of seven; two modules were compiled and run by nothing at all, so
// every guard written for one of them had only ever passed in the terminal that
// wrote it; and three whole tiers - shell, Ansible, OpenTofu - sat outside it
// with nothing saying so. Nobody removed those checks. They were never added,
// and **an allow list cannot tell the difference between a check that is absent
// and a check that is fine**.
//
// So the units are enumerated from the filesystem, not from a list. A unit that
// nothing executes fails, unless it is named in tests/coverage-blocklist.yml -
// which is a debt with a ceiling that may only fall, not an exemption with a
// reason.
//
// TESTED MEANS EXECUTED, NOT READ. A test that greps a script's text is a
// change detector: it passes forever while the behaviour rots. This repository
// refuses those by name, and six of its seven shell scripts were in exactly
// that state - including the gate that enforces coverage, the script that sets
// up every machine, and all three git hooks.

type coverageBlocklist struct {
	Ceiling int      `yaml:"ceiling"`
	Blocked []string `yaml:"blocked"`
}

// A unit is "<tier>:<path>", so one flat list covers every tier and a typo in
// the tier is as visible as a typo in the path.
type unit struct {
	tier, path string
}

func (u unit) String() string { return u.tier + ":" + u.path }

// executesIt reports whether any test in the repository RUNS the given path,
// rather than reading it. Matching on the command forms the test tiers actually
// use: exec.Command with the path as an argument, or a shell invocation of it.
func executesIt(t *testing.T, testSources map[string]string, path string) bool {
	t.Helper()
	base := filepath.Base(path)
	// An exec of the thing, or an interpreter invoked on it. The word
	// boundaries are load-bearing: without one on the interpreter, `sh` matched
	// the tail of "push" and "finish", so a sentence of prose about the
	// pre-push hook counted as running it. That is the exact false signal this
	// whole file exists to refuse, produced by the file itself on its first
	// run - which is why the check is anchored and comments are skipped.
	run := regexp.MustCompile(`(?:exec\.Command|exec\.CommandContext)\([^)]*\b` + regexp.QuoteMeta(base) + `\b|` +
		`\b(?:bash|sh|ansible-playbook)\s+[^\n"]*\b` + regexp.QuoteMeta(base) + `\b`)
	for _, body := range testSources {
		for _, line := range strings.Split(body, "\n") {
			trimmed := strings.TrimSpace(line)
			// Prose describing a script is not running it, and this file's
			// whole premise is that reading is not testing.
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if strings.Contains(line, base) && run.MatchString(line) {
				return true
			}
		}
	}
	return false
}

// testSources is every file in the repository that is a test.
func testSources(t *testing.T) map[string]string {
	t.Helper()
	root := repoRoot(t)
	out := map[string]string{}
	for _, rel := range trackedMatching(t, func(rel string) bool {
		if !authoredHere(rel) {
			return false
		}
		return strings.HasSuffix(rel, "_test.go") || strings.HasSuffix(rel, ".test.ts") ||
			strings.HasSuffix(rel, ".spec.ts") || strings.HasSuffix(rel, ".tftest.hcl")
	}) {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		out[rel] = string(body)
	}
	if len(out) == 0 {
		t.Fatal("found no test files at all, so every unit would look uncovered")
	}
	return out
}

// tracked lists this repository's own files matching a predicate.
//
// It used to walk from the root with a skipDirs map, because the mutation
// ledger's scratch tree was not a git repository and `git ls-files` there
// exited 128. The ledger runs `git init` in that copy now, so this is the same
// enumeration everything else uses.
func tracked(t *testing.T, keep func(rel string) bool) []string {
	t.Helper()
	return trackedMatching(t, func(rel string) bool {
		return authoredHere(rel) && keep(rel)
	})
}

var assertsSomething = regexp.MustCompile(`(?m)^\s*(validation\s*\{|precondition\s*\{|postcondition\s*\{|check\s+")`)

// discover enumerates every unit this repository is expected to test.
func discover(t *testing.T) []unit {
	t.Helper()
	root := repoRoot(t)
	var units []unit

	// Go: one unit per module under scripts/. tests/go is test code, and
	// measuring how thoroughly the tests test themselves is a number with no
	// meaning - the coverage baseline has said so since it was written.
	for _, m := range goModules(t) {
		units = append(units, unit{"go", "scripts/" + m})
	}

	// Shell: every script the repository ships, wherever it lives.
	for _, p := range tracked(t, func(rel string) bool {
		return strings.HasSuffix(rel, ".sh") || strings.HasPrefix(rel, "githooks/")
	}) {
		units = append(units, unit{"shell", p})
	}

	// Ansible: a YAML that actually plays something. requirements.yml declares
	// collections and executes nothing, so it is not a unit.
	for _, p := range tracked(t, func(rel string) bool {
		return strings.HasPrefix(rel, "management/hypervisor/") && strings.HasSuffix(rel, ".yml")
	}) {
		body, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			t.Fatalf("reading %s: %v", p, err)
		}
		if regexp.MustCompile(`(?m)^\s*-?\s*(hosts|tasks):`).Match(body) {
			units = append(units, unit{"ansible", p})
		}
	}

	// OpenTofu: a file is a unit when it asserts something. A file that only
	// declares resources is exercised by any plan; a file with a validation or
	// a precondition is making a claim that something should assert fails.
	for _, p := range tracked(t, func(rel string) bool { return strings.HasSuffix(rel, ".tf") }) {
		body, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			t.Fatalf("reading %s: %v", p, err)
		}
		if assertsSomething.Match(body) {
			units = append(units, unit{"tofu", p})
		}
	}

	return units
}

// covered decides whether a unit is executed by something in the test suite.
func covered(t *testing.T, u unit, sources map[string]string, floors map[string]float64) bool {
	t.Helper()
	switch u.tier {
	case "go":
		// Run by `task test:go`'s sweep, and its depth is held by a floor.
		// TestEveryGoModuleHasACoverageFloor owns the second half.
		_, hasFloor := floors["go/"+filepath.Base(u.path)]
		return hasFloor

	case "shell", "ansible":
		return executesIt(t, sources, u.path)

	case "tofu":
		// Exercised when a .tftest.hcl expects one of its declared identifiers
		// to fail. A plan alone runs the file; only expect_failures proves the
		// assertion was ever reached.
		body, err := os.ReadFile(filepath.Join(repoRoot(t), u.path))
		if err != nil {
			t.Fatalf("reading %s: %v", u.path, err)
		}
		// Every declaration in the file that refuses something, and each
		// has to be one a test of this root expects to fail. Read from the
		// tests' expect_failures lists, not from their text: a test that
		// merely mentions a name, or lists nothing, has not seen it refuse.
		expected := map[string]bool{}
		for name, src := range sources {
			// Its own root's tests, and no other's. A test runs the root
			// it sits in, so one in another root that happens to name the
			// same identifier never reached this file's assertion - which
			// is how a second root's `variable "site"` validation counted
			// as exercised by the first root's test of its own.
			if strings.HasSuffix(name, ".tftest.hcl") && testsTheRootOf(name, u.path) {
				for _, addr := range expectedFailures(src) {
					expected[addr] = true
				}
			}
		}
		asserting := assertingDeclarations(string(body))
		for _, addr := range asserting {
			if !expected[addr] {
				return false
			}
		}
		return len(asserting) > 0
	}
	t.Fatalf("unit %s has a tier nothing knows how to check", u)
	return false
}

var (
	tofuBlockHeader = regexp.MustCompile(`^(variable|output|check|resource|data)\s+"([^"]+)"(?:\s+"([^"]+)")?\s*\{`)
	expectFailures  = regexp.MustCompile(`(?s)expect_failures\s*=\s*\[([^\]]*)\]`)
)

// assertingDeclarations is the address of every top-level block in an
// OpenTofu file that refuses something - a validation, a precondition, a
// postcondition, or a check - as a test names it in expect_failures.
func assertingDeclarations(body string) []string {
	var out []string
	var header []string
	var block strings.Builder
	flush := func() {
		if header == nil {
			return
		}
		if header[1] == "check" || assertsSomething.MatchString(block.String()) {
			switch header[1] {
			case "variable":
				out = append(out, "var."+header[2])
			case "output", "check":
				out = append(out, header[1]+"."+header[2])
			case "resource":
				out = append(out, header[2]+"."+header[3])
			case "data":
				out = append(out, "data."+header[2]+"."+header[3])
			}
		}
		header = nil
		block.Reset()
	}
	for _, line := range strings.Split(body, "\n") {
		if m := tofuBlockHeader.FindStringSubmatch(line); m != nil {
			flush()
			header = m
			continue
		}
		if strings.HasPrefix(line, "}") {
			flush()
			continue
		}
		if code := strings.TrimSpace(line); header != nil && !strings.HasPrefix(code, "#") && !strings.HasPrefix(code, "//") {
			block.WriteString(line)
			block.WriteByte('\n')
		}
	}
	flush()
	sort.Strings(out)
	return out
}

// expectedFailures is every address a test file lists in an expect_failures,
// comments aside.
func expectedFailures(src string) []string {
	var code strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if t := strings.TrimSpace(line); !strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "//") {
			code.WriteString(line)
			code.WriteByte('\n')
		}
	}
	var out []string
	for _, m := range expectFailures.FindAllStringSubmatch(code.String(), -1) {
		for _, addr := range strings.Split(m[1], ",") {
			if addr = strings.TrimSpace(addr); addr != "" {
				out = append(out, addr)
			}
		}
	}
	return out
}

// testsTheRootOf reports whether a .tftest.hcl file runs the root or module an
// OpenTofu file belongs to: the test sits in that directory, or in the tests/
// directory directly beneath it.
func testsTheRootOf(test, file string) bool {
	dir := filepath.Dir(test)
	if filepath.Base(dir) == "tests" {
		dir = filepath.Dir(dir)
	}
	return filepath.ToSlash(dir) == filepath.ToSlash(filepath.Dir(file))
}

// blocklistEntries is the declared debt, shared by every guard that can defer
// something into it. One list and one ceiling, so the total debt is a number
// somebody can look at rather than a set of separate allowances.
func blocklistEntries(t *testing.T) map[string]bool {
	t.Helper()
	var list coverageBlocklist
	if err := yaml.Unmarshal([]byte(readRepoFile(t, "tests/coverage-blocklist.yml")), &list); err != nil {
		t.Fatalf("parsing tests/coverage-blocklist.yml: %v", err)
	}
	out := map[string]bool{}
	for _, b := range list.Blocked {
		out[strings.TrimSpace(b)] = true
	}
	return out
}

func TestEveryUnitIsExecutedByATestOrIsADeclaredDebt(t *testing.T) {
	var list coverageBlocklist
	if err := yaml.Unmarshal([]byte(readRepoFile(t, "tests/coverage-blocklist.yml")), &list); err != nil {
		t.Fatalf("parsing tests/coverage-blocklist.yml: %v", err)
	}

	var floors struct {
		Baselines map[string]float64 `json:"baselines"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, "tests/coverage-baseline.json")), &floors); err != nil {
		t.Fatalf("parsing tests/coverage-baseline.json: %v", err)
	}

	blocked := map[string]bool{}
	for _, b := range list.Blocked {
		blocked[strings.TrimSpace(b)] = true
	}

	// The ceiling may only fall, and it is the count rather than a number
	// somebody keeps in step by hand.
	if len(list.Blocked) > list.Ceiling {
		t.Errorf(`tests/coverage-blocklist.yml has %d entries and a ceiling of %d.

The ceiling may only fall. Adding an entry means something stopped being
tested, or arrived untested - either way it is a debt and it does not get to
raise its own limit.`, len(list.Blocked), list.Ceiling)
	}

	sources := testSources(t)
	units := discover(t)
	if len(units) == 0 {
		t.Fatal("discovered no units at all, so this asserts nothing")
	}

	var uncovered, stale []string
	for _, u := range units {
		isCovered := covered(t, u, sources, floors.Baselines)
		switch {
		case isCovered && blocked[u.String()]:
			stale = append(stale, u.String())
		case !isCovered && !blocked[u.String()]:
			uncovered = append(uncovered, u.String())
		}
	}
	sort.Strings(uncovered)
	sort.Strings(stale)

	if len(uncovered) > 0 {
		t.Errorf(`%d unit(s) are executed by no test and are not declared as debt:

  %s

Write a test that RUNS it - reading its source is a change detector, which
passes forever while the behaviour rots. If it genuinely cannot be tested yet,
add it to tests/coverage-blocklist.yml and raise the ceiling in the same
commit, which makes the debt visible instead of invisible.`,
			len(uncovered), strings.Join(uncovered, "\n  "))
	}

	if len(stale) > 0 {
		t.Errorf(`%d unit(s) are on the block list and are now tested:

  %s

Remove them and lower the ceiling by the same number. A block list that keeps
entries it no longer needs is slack, and slack is how a coverage baseline here
once sat ten points below the real figure without anybody noticing.`,
			len(stale), strings.Join(stale, "\n  "))
	}
}

func TestAssertingDeclarationsAndExpectedFailuresAreReadNotMatched(t *testing.T) {
	body := `variable "site" {
  type = string
  validation {
    condition = true
  }
}

variable "plain" {
  type = string
}

output "grants" {
  value = 1
  precondition {
    condition = true
  }
}

resource "terraform_data" "invariants" {
  lifecycle {
    precondition {
      condition = true
    }
  }
}

resource "terraform_data" "quiet" {
  # validation { would be here }
}

data "thing" "read" {
  lifecycle {
    postcondition {
      condition = true
    }
  }
}

check "reachable" {
}
`
	want := []string{"check.reachable", "data.thing.read", "output.grants", "terraform_data.invariants", "var.site"}
	if got := assertingDeclarations(body); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("asserting declarations are %v, want %v", got, want)
	}

	test := `run "a" {
  expect_failures = [var.site]
}
run "b" {
  # expect_failures = [var.commented]
  expect_failures = [
    terraform_data.invariants,
    output.grants,
  ]
}
run "c" {
  expect_failures = []
}
# a run that mentions var.plain and the word expect_failures expects nothing
`
	wantExpected := []string{"var.site", "terraform_data.invariants", "output.grants"}
	if got := expectedFailures(test); strings.Join(got, " ") != strings.Join(wantExpected, " ") {
		t.Errorf("expected failures are %v, want %v", got, wantExpected)
	}
}
