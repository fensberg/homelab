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

var testDeclaration = regexp.MustCompile(`(?m)^func (Test|Benchmark)[A-Z_]`)

// Fuzz targets are counted as their own tier rather than folded into the one
// they happen to live in. They answer a different question - what happens on
// input nobody thought of - and their number is exactly the kind that goes
// quietly to zero, because deleting a fuzz target looks like tidying up a test
// that never fails.
var fuzzDeclaration = regexp.MustCompile(`(?m)^func Fuzz[A-Z_]`)

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
		// Wherever it lives. A fuzz target beside the unit tests is still a
		// fuzz target, and the interesting number is how many exist at all.
		counts["fuzz"] += len(fuzzDeclaration.FindAll(body, -1))
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

// The tiers declared in tests/README.md are exactly the ones measured.
//
// WHAT THIS CLOSES. Everything built here makes each tier fail closed - units
// enumerated from the filesystem, estate surfaces from the repository,
// uncovered functions from the coverage profile - and all of it answers
// *within* a tier. Nothing checked that the LIST of tiers was complete, which
// is an allow list in exactly the way this repository spent a day removing
// everywhere else (#310).
//
// It had already drifted, which is the useful part: tests/README.md declared
// five tiers while the census counted eight. tofu, js and fuzz were measured,
// floored and reported every run, and absent from the one place a reader looks
// to find out what tiers exist.
//
// WHAT IT CANNOT DO, said plainly. It cannot make anybody think of a MISSING
// pillar - nothing can. Disaster recovery, upgrade and migration, capacity, and
// failure injection are all pillars this estate has no tier for, and no check
// will produce them. What this makes impossible is a pillar that exists and is
// not written down, which is the half that is mechanisable.
func TestTheDeclaredTiersAreTheMeasuredOnes(t *testing.T) {
	readme := readRepoFile(t, "tests/README.md")

	// The bold name in the first column of the tiers table.
	rows := regexp.MustCompile(`(?m)^\|\s*\*\*([a-z0-9-]+)\*\*\s*\|`).FindAllStringSubmatch(readme, -1)
	declared := map[string]bool{}
	for _, m := range rows {
		declared[m[1]] = true
	}

	const atLeastFiveTiers = 5
	if len(declared) < atLeastFiveTiers {
		t.Fatalf(`only %d tier(s) were read out of tests/README.md, and this repository
has more than that.

The table's shape has changed, so this check has stopped reading the thing it
claims to check - which looks exactly like a repository whose tiers all agree.`,
			len(declared))
	}

	var baseline struct {
		Tiers map[string]int `json:"tiers"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, "tests/coverage-baseline.json")), &baseline); err != nil {
		t.Fatalf("parsing tests/coverage-baseline.json: %v", err)
	}

	counts := census(t)

	for tier := range declared {
		if _, floored := baseline.Tiers[tier]; !floored {
			t.Errorf(`tests/README.md declares the tier %q and tests/coverage-baseline.json
has no floor for it.

So nothing stops that tier going to zero. A tier with a name and no number is
a tier nobody is counting.`, tier)
		}
		if counts[tier] == 0 {
			t.Errorf(`tests/README.md declares the tier %q and the census counts no tests
in it.

Either the tier is aspirational, in which case saying so is better than listing
it beside seven that exist, or the census cannot see it - which means its tests
are being counted as some other tier, or not at all.`, tier)
		}
	}

	for tier := range baseline.Tiers {
		if !declared[tier] {
			t.Errorf(`the tier %q is floored in tests/coverage-baseline.json and counted by
the census, and tests/README.md does not mention it.

That is the allow-list failure one level up: the table is where somebody looks
to find out what tiers exist, and a tier missing from it is invisible in
exactly the way a missing check is. Add a row saying what it answers and what
it may touch.`, tier)
		}
	}
}
