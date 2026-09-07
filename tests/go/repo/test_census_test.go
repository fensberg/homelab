package repo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// How many tests each tier has, and a floor under each.
//
// WHY COUNT AT ALL, when coverage percentages exist. Because they only exist
// for one tier. Statement coverage answers "how much of this Go program did
// the tests execute", which is a fair question for a unit test and a
// meaningless one for a tier whose job is to drive real infrastructure: an
// integration test that provisions a cluster and asserts five things about it
// covers almost no Go statements and is worth more than a hundred that do.
//
// So the higher tiers have no percentage, and had no number of any kind. What
// they have instead is a count and a floor.
//
// THE REASON THIS MATTERS, in the operator's words: "Unit tests are real cheap
// and real easy - API, e2e, integration, etc. tests are hard. I want to see
// actual numbers for these and I don't want THOSE to go forgotten."
//
// The measurement makes the point. On the day this was written the unit and
// contract tiers held 511 tests between them and the three tiers that touch a
// real estate held 25. That ratio is not wrong - the hard tiers are hard, and
// most of what this repository asserts genuinely is a repository invariant -
// but it is exactly the ratio nobody notices sliding further, because the
// cheap number always goes up.
//
// A floor may only be lowered deliberately, in the same commit, the way
// tests/coverage-baseline.json already says a coverage floor may. Consolidating
// five test functions into one table is a real improvement and will drop a
// count; that is a decision worth writing down rather than a failure. What the
// floor makes impossible is the count dropping and nobody noticing.

var testDeclaration = regexp.MustCompile(`(?m)^func (Test|Fuzz|Benchmark)[A-Z_]`)

var buildTag = regexp.MustCompile(`(?m)^//go:build\s+([a-z0-9_]+)`)

// tier decides which tier a Go test file belongs to. A build tag wins, because
// that is what actually decides whether the file compiles into a run.
func tierOf(rel string, body []byte) string {
	if m := buildTag.FindSubmatch(body); m != nil {
		tag := string(m[1])
		switch tag {
		case "integration", "api", "e2e":
			return tag
		}
	}
	if strings.HasPrefix(rel, "scripts/") {
		return "unit"
	}
	if strings.HasPrefix(rel, "tests/go/") {
		return "contract"
	}
	return ""
}

func census(t *testing.T) map[string]int {
	t.Helper()
	root := repoRoot(t)
	counts := map[string]int{}

	for _, rel := range tracked(t, func(rel string) bool {
		return strings.HasSuffix(rel, "_test.go")
	}) {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		if tier := tierOf(rel, body); tier != "" {
			counts[tier] += len(testDeclaration.FindAll(body, -1))
		}
	}

	// JavaScript, where a test is an `it(` or a `test(` call.
	jsCase := regexp.MustCompile(`(?m)^\s*(it|test)\s*\(`)
	for _, rel := range tracked(t, func(rel string) bool {
		return strings.HasPrefix(rel, "tests/js/") &&
			(strings.HasSuffix(rel, ".test.ts") || strings.HasSuffix(rel, ".spec.ts"))
	}) {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		counts["js"] += len(jsCase.FindAll(body, -1))
	}

	// OpenTofu, where a test is a run block.
	runBlock := regexp.MustCompile(`(?m)^run\s+"`)
	for _, rel := range tracked(t, func(rel string) bool {
		return strings.HasSuffix(rel, ".tftest.hcl")
	}) {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		counts["tofu"] += len(runBlock.FindAll(body, -1))
	}

	return counts
}

func TestNoTierQuietlyLosesTests(t *testing.T) {
	var baseline struct {
		Tiers map[string]int `json:"tiers"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, "tests/coverage-baseline.json")), &baseline); err != nil {
		t.Fatalf("parsing tests/coverage-baseline.json: %v", err)
	}
	if len(baseline.Tiers) == 0 {
		t.Fatal(`tests/coverage-baseline.json declares no tier floors, so nothing stops a whole tier being deleted`)
	}

	counts := census(t)

	// Reported every run rather than only on failure. A number nobody sees is
	// a number nobody acts on, which is the whole complaint the tier floors
	// exist to answer.
	var table strings.Builder
	table.WriteString("### Tests by tier\n\n| Tier | Tests | Floor |\n| --- | --- | --- |\n")
	tiers := make([]string, 0, len(baseline.Tiers))
	for tier := range baseline.Tiers {
		tiers = append(tiers, tier)
	}
	sort.Strings(tiers)
	for _, tier := range tiers {
		fmt.Fprintf(&table, "| %s | %d | %d |\n", tier, counts[tier], baseline.Tiers[tier])
	}
	t.Log("\n" + table.String())
	if summary := os.Getenv("GITHUB_STEP_SUMMARY"); summary != "" {
		f, err := os.OpenFile(summary, os.O_APPEND|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintln(f, table.String())
			f.Close()
		}
	}

	for _, tier := range tiers {
		floor := baseline.Tiers[tier]
		if counts[tier] < floor {
			t.Errorf(`the %s tier has %d test(s) and its floor is %d.

Tests were removed, or a file stopped being recognised as belonging to this
tier. Neither is necessarily wrong - consolidating several test functions into
one table is a real improvement and will drop the count - but it is a decision
somebody makes on purpose, so lower the floor in tests/coverage-baseline.json
in this same change and say why in the message.

What this refuses is the count dropping and nobody noticing, which is how a
tier that is hard to write quietly stops being written.`, tier, counts[tier], floor)
		}
	}

	// A tier the census finds and the baseline does not floor is a tier
	// nothing is watching.
	for tier := range counts {
		if _, ok := baseline.Tiers[tier]; !ok {
			t.Errorf("the census found %d test(s) in a %q tier that tests/coverage-baseline.json does not floor, so nothing would notice it emptying", counts[tier], tier)
		}
	}
}
