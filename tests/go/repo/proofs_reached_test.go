package repo

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
	"testing"

	"homelab/tests/parsed"
)

// The ledger breaks every guarded file and runs its guard, which is half an
// hour of runners, and on a pull request most of that was asking questions the
// change could not have altered. So a pull request proves what it could have
// changed, and main proves everything after the merge (epoch 16, decision 2).
//
// A proof is reached by the file it breaks, the place it is written, the guard
// it proves, or the rule it names. What this cannot see is a helper a guard
// calls, so a change to a helper is answered on main. A change to the ledger's
// own machinery reaches everything: nothing else could say the machinery works.

// changedSinceEnv is the commit a pull request's run compares itself with.
// Unset, as on main or at a terminal, the whole ledger runs.
const changedSinceEnv = "PROOFS_CHANGED_SINCE"

// reach is which proofs a change could have altered the answer of.
type reach struct {
	whole  bool
	files  map[string]bool
	guards map[string]bool
	rules  map[string]bool
	// harnesses are the rule harnesses whose shared files changed, so every
	// rule they run is reached.
	harnesses map[string]bool
}

// proofMachinery is what every proof depends on.
func proofMachinery(rel string) bool {
	return strings.HasPrefix(rel, "tests/go/parsed/") || rel == "tests/go/repo/mutation_ledger_test.go"
}

// reachOf reads a change: the paths it touched, and the guards each declares.
func reachOf(changed []string, guardsIn func(rel string) []string) reach {
	r := reach{files: map[string]bool{}, guards: map[string]bool{}, rules: map[string]bool{}, harnesses: map[string]bool{}}
	for _, rel := range changed {
		r.files[rel] = true
		if proofMachinery(rel) {
			r.whole = true
		}
		for _, g := range guardsIn(rel) {
			r.guards[g] = true
		}
		rest, ok := strings.CutPrefix(rel, conftestRules+"/rules/")
		switch {
		case ok:
			r.rules[strings.SplitN(rest, "/", 2)[0]] = true
		case strings.HasPrefix(rel, semgrepRules+"/"):
			r.harnesses[codeRulesHarness] = true
		case strings.HasPrefix(rel, conftestRules+"/"):
			r.harnesses[configurationRulesHarness] = true
		}
	}
	return r
}

// holds reports whether the change could have altered this proof's answer.
func (r reach) holds(m mutation) bool {
	written, _, _ := strings.Cut(m.Source, "#")
	return r.whole ||
		r.files[m.File] ||
		r.files[written] ||
		r.files[ledgerEntries+"/"+written] ||
		r.guards[m.Guard] ||
		(m.Rule != "" && r.rules[m.Rule]) ||
		r.harnesses[m.Guard]
}

// guardsDeclaredIn is the guards a Go test file declares.
func guardsDeclaredIn(t *testing.T, tree *parsed.Tree, rel string) []string {
	t.Helper()
	if !strings.HasSuffix(rel, "_test.go") || !tree.Exists(rel) {
		return nil
	}
	var guards []string
	for _, d := range tree.Go(t, token.NewFileSet(), rel).Decls {
		if f, ok := d.(*ast.FuncDecl); ok && strings.HasPrefix(f.Name.Name, "Test") {
			guards = append(guards, f.Name.Name)
		}
	}
	return guards
}

// changeSince is the change since a commit, or the whole ledger when none is
// named. A commit it cannot compare with is an error, which the ledger fails
// on: passing would say the change was proved.
func changeSince(t *testing.T, tree *parsed.Tree, since string) (reach, error) {
	t.Helper()
	if since == "" {
		return reach{whole: true}, nil
	}
	out, errs, err := tree.Invoke(parsed.Run{}, "git", "diff", "--name-only", since, "HEAD")
	if err != nil {
		return reach{}, fmt.Errorf("%s is %s, and what changed since it could not be read: %w\n%s", changedSinceEnv, since, err, errs)
	}
	return reachOf(strings.Fields(string(out)), func(rel string) []string { return guardsDeclaredIn(t, tree, rel) }), nil
}

func TestAChangeReachesTheProofsItCouldAlter(t *testing.T) {
	light(t)
	declares := func(rel string) []string {
		if rel == "tests/go/repo/x_test.go" {
			return []string{"TestX"}
		}
		return nil
	}
	proof := mutation{Guard: "TestX", File: "scripts/thing.sh", Source: "TestX.one.yml"}
	rule := mutation{Guard: configurationRulesHarness, Rule: "some_rule", File: "scripts/other.yml", Source: "TestTheConfigurationRulesHold.a.yml"}
	code := mutation{Guard: codeRulesHarness, Rule: "some-id", File: "scripts/other.yml", Source: "TestTheCodeRulesHold.a.yml"}
	inApp := mutation{Guard: "TestY", File: "modules/applications/a/x", Source: "modules/applications/a/tests/mutations.yml#3"}

	for _, c := range []struct {
		name    string
		changed []string
		proof   mutation
		want    bool
	}{
		{"the file it breaks", []string{"scripts/thing.sh"}, proof, true},
		{"its own file", []string{"tests/mutations/TestX.one.yml"}, proof, true},
		{"the file declaring its guard", []string{"tests/go/repo/x_test.go"}, proof, true},
		{"a place in an application's ledger", []string{"modules/applications/a/tests/mutations.yml"}, inApp, true},
		{"the rule it names", []string{"policy/conftest/rules/some_rule/some_rule.rego"}, rule, true},
		{"another rule", []string{"policy/conftest/rules/other_rule/other_rule.rego"}, rule, false},
		{"what every configuration rule shares", []string{"policy/conftest/lib/lib.rego"}, rule, true},
		{"the code rules", []string{"policy/semgrep/credentials.yml"}, code, true},
		{"the code rules, for a configuration rule", []string{"policy/semgrep/credentials.yml"}, rule, false},
		{"the ledger's own machinery", []string{"tests/go/repo/mutation_ledger_test.go"}, proof, true},
		{"the parsers", []string{"tests/go/parsed/proofs.go"}, proof, true},
		{"nothing of its own", []string{"docs/epochs/README.md"}, proof, false},
		{"nothing at all", nil, proof, false},
	} {
		if got := reachOf(c.changed, declares).holds(c.proof); got != c.want {
			t.Errorf("%s: a change to %v reaching %s is %v, want %v", c.name, c.changed, c.proof.Source, got, c.want)
		}
	}
}

// No change named is main's run, so it proves the whole ledger; a change named
// and unreadable is not a pass, and proves nothing by accident.
func TestARunProvesTheWholeLedgerOrTheChangeItCanRead(t *testing.T) {
	light(t)
	tree := repository(t)
	whole, err := changeSince(t, tree, "")
	if err != nil || !whole.holds(mutation{Guard: "TestAnything"}) {
		t.Errorf("with no change named, a proof was left out of the run: %v", err)
	}
	if _, err := changeSince(t, tree, "a-commit-nobody-made"); err == nil {
		t.Error("a commit that cannot be compared with was read as a change")
	}
}
