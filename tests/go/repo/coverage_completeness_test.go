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
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, "_test.go") && !strings.HasSuffix(name, ".test.ts") &&
			!strings.HasSuffix(name, ".spec.ts") && !strings.HasSuffix(name, ".tftest.hcl") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, _ := filepath.Rel(root, path)
		out[rel] = string(body)
		return nil
	})
	if err != nil {
		t.Fatalf("walking for test sources: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("found no test files at all, so every unit would look uncovered")
	}
	return out
}

// tracked lists repository files matching a predicate.
//
// A walk with skipDirs rather than `git ls-files`, because the mutation ledger
// runs this guard against a copy of the tracked files that is not itself a git
// repository - asking git there fails with exit 128 and the guard reports the
// harness instead of the code. Found by the ledger refusing the entry.
func tracked(t *testing.T, keep func(rel string) bool) []string {
	t.Helper()
	root := repoRoot(t)
	var matched []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if keep(rel) {
			matched = append(matched, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
	sort.Strings(matched)
	return matched
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
		decls := regexp.MustCompile(`(?m)^(?:variable|resource\s+"[^"]+"|terraform_data)\s+"([^"]+)"`).
			FindAllStringSubmatch(string(body), -1)
		for name, src := range sources {
			if !strings.HasSuffix(name, ".tftest.hcl") {
				continue
			}
			for _, d := range decls {
				if strings.Contains(src, "expect_failures") && strings.Contains(src, d[1]) {
					return true
				}
			}
		}
		return false
	}
	t.Fatalf("unit %s has a tier nothing knows how to check", u)
	return false
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
