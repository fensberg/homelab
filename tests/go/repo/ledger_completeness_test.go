package repo

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Every guard in this package is either proved or declared unprovable.
//
// The ledger worked, and was still useless, because nothing required anything
// to be in it. Six guards were added in one session - the Go-invocation check,
// the SKIP-list check, the gate-ordering check, two version-pin checks and the
// workstation-routing check - each verified by hand and none of those proofs
// committed. The ledger sat at seven entries, all from before.
//
// That is the same failure as the rule it replaced: it depended on somebody
// remembering. This is the part that does not.
//
// It does not require every test to be mutation-provable. Some genuinely are
// not - a file mode, a pure function whose table already contains the failing
// case - and forcing an entry for those would produce ceremony rather than
// evidence. What it requires is that the decision was made and written down.

type ledgerCompleteness struct {
	OwedCeiling int `yaml:"owed_ceiling"`
	Mutations   []struct {
		Guard string `yaml:"guard"`
	} `yaml:"mutations"`
	Unprovable []struct {
		Test   string `yaml:"test"`
		Path   string `yaml:"path"`
		Reason string `yaml:"reason"`
		Owed   bool   `yaml:"owed"`
	} `yaml:"unprovable"`
}

func TestEveryGuardIsProvedOrDeclaredUnprovable(t *testing.T) {
	t.Parallel()
	skipIfInner(t)
	tree := repository(t)

	var l ledgerCompleteness
	tree.YAMLInto(t, ledgerEntries+".yml", &l)

	proved := map[string]bool{}
	for _, m := range readLedger(t).Mutations {
		proved[m.Guard] = true
	}
	exemptTest := map[string]bool{}
	exemptPath := map[string]bool{}
	owed := 0
	for _, u := range l.Unprovable {
		if u.Owed {
			owed++
		} else if strings.TrimSpace(u.Reason) == "" {
			t.Errorf("the unprovable entry for %q%q gives no reason, which makes it an "+
				"exemption nobody has to justify", u.Test, u.Path)
		}
		if u.Test != "" {
			exemptTest[u.Test] = true
		}
		if u.Path != "" {
			exemptPath[filepath.Base(u.Path)] = true
		}
	}

	var undeclared []string
	seen := map[string]bool{}
	fset := token.NewFileSet()
	for _, rel := range tree.Files(t) {
		if filepath.Dir(rel) != guardsDir || !strings.HasSuffix(rel, "_test.go") {
			continue
		}
		file := filepath.Base(rel)
		for _, d := range tree.Go(t, fset, rel).Decls {
			fn, ok := d.(*ast.FuncDecl)
			// TestMain starts the package and is no test of anything.
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") || fn.Name.Name == "TestMain" {
				continue
			}
			name := fn.Name.Name
			seen[name] = true
			if proved[name] || exemptTest[name] || exemptPath[file] {
				continue
			}
			undeclared = append(undeclared, file+" "+name)
		}
	}

	if problem := owedCeilingProblem(owed, l.OwedCeiling); problem != "" {
		t.Error(problem)
	}

	if len(seen) == 0 {
		t.Fatal("no test functions were found in tests/go/repo, so this checked nothing")
	}

	sort.Strings(undeclared)
	if len(undeclared) > 0 {
		t.Errorf(`%d guard(s) in tests/go/repo are neither proved by the ledger nor
declared unprovable:

  %s

For each: add a mutations entry that breaks what it guards and requires it to
fail, or add it under unprovable: with a reason. "The table already contains a
failing case" is a good reason; silence is not.`,
			len(undeclared), strings.Join(undeclared, "\n  "))
	}

	// A name here that matches nothing is an exemption for a test that has been
	// renamed or deleted - which is how a list stops describing the thing it
	// is about.
	for _, u := range l.Unprovable {
		if u.Test != "" && !seen[u.Test] {
			t.Errorf("unprovable names %q, which is not a test in tests/go/repo", u.Test)
		}
	}
	for _, m := range l.Mutations {
		if !seen[m.Guard] {
			t.Errorf("the ledger names guard %q, which is not a test in tests/go/repo", m.Guard)
		}
	}
}

// owedCeilingProblem is what is wrong with an owed count against its
// ceiling, or "". The debt may shrink, never grow: without a ceiling the owed
// list is a place to put any new guard nobody wants to prove. And the ceiling
// may not sit above what is owed, because slack is room the next new guard
// spends unproved - a proof landed on #773 and the ceiling was left one above
// the count, which a log line said only when something else had failed.
func owedCeilingProblem(owed, ceiling int) string {
	switch {
	case owed > ceiling:
		return fmt.Sprintf(`%d guards are marked owed; the ceiling is %d.

owed means "provable, not yet proved". It is bounded on purpose: a new guard
cannot join that list without another one leaving it. Write the mutation entry
for this one, or prove an older one and lower the ceiling in the same diff.`, owed, ceiling)
	case owed < ceiling:
		return fmt.Sprintf("%d guards owed against a ceiling of %d - lower it to %d. A ceiling above "+
			"what is owed is room the next new guard spends without being proved.", owed, ceiling, owed)
	}
	return ""
}

func TestTheOwedCeilingFitsTheDebtExactly(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		owed, ceiling int
		want          string
	}{
		{24, 24, ""},
		{25, 24, "the ceiling is 24"},
		{23, 24, "lower it to 23"},
	} {
		got := owedCeilingProblem(c.owed, c.ceiling)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%d owed against %d: got %q, want %q", c.owed, c.ceiling, got, c.want)
		}
	}
}
