package repo

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"testing"

	"homelab/tests/parsed"
)

// The configuration rules are held to their own tests the way the Go is held
// to its coverage floor, and harder: every line of every policy file is run
// by a test, with no list of lines excused. A refusal nothing tests is one
// that may never fire, and the ledger cannot tell: it proves a rule refuses a
// broken tree somewhere, not that each of its refusals does (agreed
// 2026-10-25, after 162 lines across 44 files were found untested).
//
// RUN MEANS MADE. OPA counts a rule's first line - `deny contains msg if` -
// as run only when the rule produced a value; a test that reaches every
// condition of a refusal and satisfies none runs its body and not its head.
// So every line run is every refusal made by some test, and every helper
// given a value, and one guard holds both.

// regoCoverage is what `opa test --coverage` reports, by file: the rows its
// tests did not run.
type regoCoverage struct {
	Files map[string]struct {
		NotCovered []rowRange `json:"not_covered"`
	} `json:"files"`
}

type rowRange struct {
	Start struct {
		Row int `json:"row"`
	} `json:"start"`
	End struct {
		Row int `json:"row"`
	} `json:"end"`
}

// policyCoverage is what the policy files' own tests leave unrun, by each
// file's path in the repository.
func policyCoverage(t *testing.T) regoCoverage {
	t.Helper()
	if _, err := exec.LookPath(opaProgram); err != nil {
		t.Fatal("opa is not on PATH; take delivery of it: scripts/take-delivery.sh opa")
	}
	stdout, stderr, err := repository(t).Invoke(parsed.Run{}, opaProgram, "test", conftestRules, "--coverage", "--format", "json")
	if err != nil {
		t.Fatalf("the policy files' own tests do not pass, so what they run of the rules cannot be read (TestEveryConfigurationRuleProvesItself says which): %v\n%s", err, stderr)
	}
	var c regoCoverage
	if err := json.Unmarshal(stdout, &c); err != nil {
		t.Fatalf("opa's coverage report could not be read: %v", err)
	}
	if len(c.Files) < 10 {
		t.Fatalf("opa's coverage report names %d policy files, which is not this repository", len(c.Files))
	}
	return c
}

func TestEveryLineOfThePolicyIsRunByItsOwnTests(t *testing.T) {
	heavy(t, "runs every policy test again with coverage, after the push hook's verify has run them")
	c := policyCoverage(t)
	files := policyFiles(t)
	var unrun []string
	for rel, tree := range files {
		if isTestPackage(tree.Package) {
			continue
		}
		cov, ok := c.Files[rel]
		if !ok {
			unrun = append(unrun, fmt.Sprintf("%s: no test runs any of it", rel))
			continue
		}
		rows := map[int]bool{}
		for _, r := range cov.NotCovered {
			for row := r.Start.Row; row <= r.End.Row; row++ {
				rows[row] = true
			}
		}
		if len(rows) > 0 {
			var listed []string
			for row := range rows {
				listed = append(listed, fmt.Sprint(row))
			}
			sort.Slice(listed, func(i, j int) bool {
				return len(listed[i]) < len(listed[j]) || len(listed[i]) == len(listed[j]) && listed[i] < listed[j]
			})
			unrun = append(unrun, fmt.Sprintf("%s: rows %s", rel, strings.Join(listed, ", ")))
		}
	}
	sort.Strings(unrun)
	if len(unrun) > 0 {
		t.Errorf("no test runs these lines of the policy, so nothing shows what they refuse:\n\n  %s\n\nWrite the test beside the rule that runs them; there is no list of lines excused.",
			strings.Join(unrun, "\n  "))
	}
}
