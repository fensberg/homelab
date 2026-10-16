package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// The configuration rules: policy/conftest, run by Conftest over every file
// of a kind tests/languages.yml says it parses.
//
// WHY THEY ARE NOT GO. The rules were Go tests that read a workflow or a
// manifest and matched text in it. That is a policy engine built by hand,
// and every such guard could be satisfied by the comment explaining the thing
// rather than by the thing. Conftest parses each file the way its own tool
// does and the rules are written over the parsed structure, so a comment is
// not there to be matched; it is the industry's tool for this, and each rule
// carries tests of its own (decided 2026-10-16).
//
// WHY THIS RUNS THEM. The mutation ledger proves every guard fails when what
// it guards is broken, on a copy of the real tree. Running Conftest from here
// keeps that proof for every rule moved: a ledger entry names this test, and
// the message of the rule that must refuse the mutation.

// conftestProgram is the policy engine, delivered by scripts/take-delivery.sh.
const conftestProgram = "conftest"

// conftestRules is where the configuration rules live.
const conftestRules = "policy/conftest"

// conftestInputs is every tracked file of a kind the declaration hands to
// Conftest.
func conftestInputs(t *testing.T) []string {
	t.Helper()
	kinds := map[string]bool{}
	for _, g := range loadLanguages(t).Governed {
		if slices.Contains(g.ParsedBy, conftestProgram) {
			kinds[g.Kind] = true
		}
	}
	if len(kinds) == 0 {
		t.Fatal("tests/languages.yml hands no kind of file to conftest, so the configuration rules would read nothing")
	}
	var files []string
	for _, rel := range trackedFiles(t) {
		if kinds[kindOf(rel)] {
			files = append(files, rel)
		}
	}
	sort.Strings(files)
	if len(files) < 100 {
		t.Fatalf("only %d files are handed to conftest, which is not this repository - the enumeration has stopped matching", len(files))
	}
	return files
}

// runConftest runs the policy engine in the repository with the given
// arguments, and gives what it said and whether it passed.
func runConftest(t *testing.T, args ...string) (string, bool) {
	t.Helper()
	if _, err := exec.LookPath(conftestProgram); err != nil {
		t.Fatal("conftest is not on PATH; take delivery of it: scripts/take-delivery.sh conftest")
	}
	cmd := exec.Command(conftestProgram, args...)
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if _, failed := err.(*exec.ExitError); !failed {
			t.Fatalf("conftest could not be run: %v", err)
		}
		return string(out), false
	}
	return string(out), true
}

func TestTheConfigurationRulesHold(t *testing.T) {
	root := repoRoot(t)
	if _, err := os.Stat(filepath.Join(root, conftestRules)); err != nil {
		t.Fatalf("%s is not there, so no configuration rule runs: %v", conftestRules, err)
	}
	// How it is run - every package, every file at once - is conftest.toml's
	// to say, so this, the lane and a person at a terminal run it alike.
	args := append([]string{"test"}, conftestInputs(t)...)
	out, ok := runConftest(t, args...)
	if !ok {
		var refused []string
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "FAIL") {
				refused = append(refused, strings.TrimSpace(line))
			}
		}
		if len(refused) == 0 {
			t.Fatalf("conftest did not run the rules:\n\n%s", out)
		}
		t.Errorf("a configuration rule refuses the repository:\n\n  %s\n\nThe rule is in %s, named by its package; its own tests beside it show what it accepts.",
			strings.Join(refused, "\n  "), conftestRules)
	}
}

// Every rule is held to what it claims by tests of its own: Conftest's
// verify runs each *_test.rego beside the rules.
func TestEveryConfigurationRuleProvesItself(t *testing.T) {
	out, ok := runConftest(t, "verify")
	if !ok {
		t.Errorf("a configuration rule's own tests fail:\n\n%s", out)
	}
	if !strings.Contains(out, "passed") {
		t.Errorf("conftest verify ran no tests, so no rule has shown it refuses anything:\n\n%s", out)
	}
}
