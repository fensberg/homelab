//go:build api

package api_test

import (
	"encoding/json"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The nine required checks are declared in two rulesets, and nothing but this
// keeps them the same.
//
// That duplication is forced rather than chosen. "Require branches to be up to
// date before merging" is a parameter of the status-check rule, not a rule of
// its own, so wanting it on `main` and not on `epoch/**` means two copies of
// the rule - and therefore two copies of the list. GitHub offers no way to
// share one.
//
// This repository's preference is consolidation over drift detection, and its
// own note says a contract test earns its place afterwards, as a guard that no
// new restatement appears. Consolidation is not available here, so this is the
// fallback: the restatement exists, and it is checked rather than trusted.
//
// The failure it prevents is quiet. Add a tenth check to one ruleset and the
// pull requests into `main` require it while the ones into an epoch branch do
// not - so work lands on the epoch branch ungated and arrives at `main` in a
// batch that has already been merged.
//
//	go test -tags=api ./api/...
const repo = "fensberg/homelab"

type ruleset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type rulesetDetail struct {
	Name       string `json:"name"`
	Conditions struct {
		RefName struct {
			Include []string `json:"include"`
		} `json:"ref_name"`
	} `json:"conditions"`
	Rules []struct {
		Type       string `json:"type"`
		Parameters struct {
			Strict bool `json:"strict_required_status_checks_policy"`
			Checks []struct {
				Context string `json:"context"`
			} `json:"required_status_checks"`
		} `json:"parameters"`
	} `json:"rules"`
}

func TestTheRequiredCheckListsAgree(t *testing.T) {
	lists := map[string][]string{}
	for _, rs := range allRulesets(t) {
		d := detail(t, rs.ID)
		for _, r := range d.Rules {
			if r.Type != "required_status_checks" {
				continue
			}
			var got []string
			for _, c := range r.Parameters.Checks {
				got = append(got, c.Context)
			}
			sort.Strings(got)
			lists[d.Name] = got
		}
	}

	require.GreaterOrEqual(t, len(lists), 1,
		"no ruleset requires any status check, so nothing gates a merge")

	if len(lists) == 1 {
		t.Log("only one ruleset declares required checks; nothing to drift, which is the " +
			"better arrangement if GitHub ever allows it")
		return
	}

	var names []string
	for n := range lists {
		names = append(names, n)
	}
	sort.Strings(names)

	first := lists[names[0]]
	for _, n := range names[1:] {
		require.Equalf(t, first, lists[n],
			"the required check lists in %q and %q have drifted.\n\n"+
				"They are duplicated because 'Require branches to be up to date before "+
				"merging' is a parameter of the status-check rule rather than a rule of "+
				"its own, so main and epoch/** need separate copies. When they disagree, "+
				"one destination silently stops requiring a check - and work lands there "+
				"ungated, then reaches main in a batch that has already been merged.",
			names[0], n)
	}
}

// The strictness is the entire reason the lists are duplicated. If it ever
// matches across both, the duplication is buying nothing and the rulesets
// should be collapsed back into one.
func TestOnlyMainRequiresBranchesToBeUpToDate(t *testing.T) {
	strict := map[string]bool{}
	targets := map[string][]string{}
	for _, rs := range allRulesets(t) {
		d := detail(t, rs.ID)
		for _, r := range d.Rules {
			if r.Type == "required_status_checks" {
				strict[d.Name] = r.Parameters.Strict
				targets[d.Name] = d.Conditions.RefName.Include
			}
		}
	}

	for name, isStrict := range strict {
		if !strictnessIsWrong(targets[name], isStrict) {
			continue
		}
		t.Errorf("%q targets %s with 'require branches to be up to date' = %v.\n\n"+
			"On main that setting is wanted: without it a pull request merges having "+
			"passed checks against a main that has since moved. On an epoch branch it "+
			"is the opposite - every merge into it marks every other open pull request "+
			"against it stale, for the months the epoch runs.",
			name, strings.Join(targets[name], ","), isStrict)
	}
}

func allRulesets(t *testing.T) []ruleset {
	t.Helper()
	var out []ruleset
	require.NoError(t, json.Unmarshal(ghAPI(t, "repos/"+repo+"/rulesets"), &out))
	return out
}

func detail(t *testing.T, id int64) rulesetDetail {
	t.Helper()
	var d rulesetDetail
	require.NoError(t, json.Unmarshal(ghAPI(t, "repos/"+repo+"/rulesets/"+itoa(id)), &d))
	return d
}

// Through `gh` rather than a raw HTTP client, so the test authenticates the
// same way everything else here does and needs no token of its own.
func ghAPI(t *testing.T, path string) []byte {
	t.Helper()
	out, err := exec.Command("gh", "api", path).Output()
	require.NoErrorf(t, err, "gh api %s - is `gh auth status` healthy?", path)
	return out
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

// --- the decisions, without GitHub -----------------------------------------
//
// The two tests above read the live rulesets, so proving they bite would mean
// misconfiguring the repository to watch them fail. These exercise the same
// decisions against constructed input, which is the part that has to be right.

func TestDriftBetweenTwoListsIsDetected(t *testing.T) {
	// Same length, different contents. An earlier version of this used lists of
	// different lengths, so a comparison that only checked length still passed
	// it - which is the weaker test failing to notice the weaker code.
	a := []string{"Sensitive Paths", "Test (go, tofu, vitest)"}
	b := []string{"Sensitive Paths", "IaC Scan (Trivy)"}
	sort.Strings(a)
	sort.Strings(b)
	if equalLists(a, b) {
		t.Fatal("two lists with the same length and different checks compared equal, so " +
			"swapping a check on one destination and not the other would go unnoticed")
	}

	longer := append(append([]string(nil), a...), "Analyze (actions)")
	if equalLists(a, longer) {
		t.Error("a list with an extra check compared equal to one without it")
	}
	if !equalLists(a, append([]string(nil), a...)) {
		t.Error("two identical lists compared unequal, which would fail the build on a " +
			"correct configuration")
	}
}

func TestStrictnessIsJudgedByWhatTheRulesetTargets(t *testing.T) {
	cases := []struct {
		name     string
		includes []string
		strict   bool
		wantBad  bool
	}{
		{"strict on main", []string{"~DEFAULT_BRANCH"}, true, false},
		{"lax on epoch", []string{"~DEFAULT_BRANCH", "refs/heads/epoch/**"}, false, false},
		{"strict reaching epoch", []string{"refs/heads/epoch/**"}, true, true},
		{"lax on main alone", []string{"~DEFAULT_BRANCH"}, false, true},
	}
	for _, c := range cases {
		if got := strictnessIsWrong(c.includes, c.strict); got != c.wantBad {
			t.Errorf("%s: judged wrong=%v, want %v", c.name, got, c.wantBad)
		}
	}
}

func equalLists(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// strictnessIsWrong is the judgement the live test makes, extracted so it can
// be exercised without a repository to misconfigure.
func strictnessIsWrong(includes []string, strict bool) bool {
	joined := strings.Join(includes, ",")
	reachesEpoch := strings.Contains(joined, "epoch")
	if strict && reachesEpoch {
		return true
	}
	return !strict && !reachesEpoch && strings.Contains(joined, "DEFAULT_BRANCH")
}
