package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

var testFunc = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(`)

func TestEveryGuardIsProvedOrDeclaredUnprovable(t *testing.T) {
	skipIfInner(t)
	root := repoRoot(t)

	body, err := os.ReadFile(filepath.Join(root, "tests", "mutations.yml"))
	if err != nil {
		t.Fatalf("reading tests/mutations.yml: %v", err)
	}
	var l ledgerCompleteness
	if err := yaml.Unmarshal(body, &l); err != nil {
		t.Fatalf("parsing tests/mutations.yml: %v", err)
	}

	proved := map[string]bool{}
	for _, m := range l.Mutations {
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

	dir := filepath.Join(root, "tests", "go", "repo")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	var undeclared []string
	seen := map[string]bool{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		for _, m := range testFunc.FindAllStringSubmatch(string(src), -1) {
			name := m[1]
			seen[name] = true
			if proved[name] || exemptTest[name] || exemptPath[e.Name()] {
				continue
			}
			undeclared = append(undeclared, e.Name()+" "+name)
		}
	}

	// The debt may shrink, never grow. Without this the owed list is a place to
	// put anything rather than a queue with an end.
	if owed > l.OwedCeiling {
		t.Errorf(`%d guards are marked owed; the ceiling is %d.

owed means "provable, not yet proved". It is bounded on purpose: a new guard
cannot join that list without another one leaving it. Write the mutation entry
for this one, or prove an older one and lower the ceiling in the same diff.`,
			owed, l.OwedCeiling)
	}
	if owed < l.OwedCeiling {
		t.Logf("%d guards owed against a ceiling of %d - lower it to %d to keep the "+
			"debt ratcheting down", owed, l.OwedCeiling, owed)
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
