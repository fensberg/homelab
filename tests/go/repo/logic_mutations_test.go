package repo

import (
	"strings"
	"testing"

	"homelab/tests/parsed"
)

// Law 3: the program's decisions are actually asserted on.
//
// Coverage proves a line ran. It proves nothing about whether anything looked
// at the result - a test that calls a function, ignores what it returns and
// asserts nothing gives full coverage and no protection, and every other check
// here is blind to it. The baseline sees an unchanged total. The exemptions
// file sees a covered function, which it is. The repository-file ledger covers
// guards over files, not Go logic. The change detector sees a function
// reachable from non-test code.
//
// So this breaks the decision and requires something to go red. Scoped to the
// logic that was deliberately extracted so it could be tested hermetically -
// which is to say, this is what confirms the extraction was worth doing.

// logicLedger is where the decisions' proofs are kept, and contractorDir the
// program they are decisions of.
const (
	logicLedger   = "tests/logic-mutations.yml"
	contractorDir = "scripts/contractor"
)

// Each entry runs on a full copy of the repository rather than just
// scripts/contractor, because the program's own contract tests find the
// repository root by walking up from their source file and read
// management/cluster and CLAUDE.md from there. A bare copy of the program
// makes six of them fail for a reason that has nothing to do with the
// mutation - which the first version of this did, and which would have made
// every entry below "fail" without proving anything.

// runContractorTests reports whether the suite passed, and which tests failed.
func runContractorTests(t *testing.T, tree *parsed.Tree) (passed bool, failed map[string]bool, output string) {
	t.Helper()
	stdout, stderr, err := tree.Invoke(parsed.Run{Dir: contractorDir}, "go", "test", "./...")
	out := append(stdout, stderr...)

	failed = map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "--- FAIL: ") {
			continue
		}
		name := strings.TrimPrefix(line, "--- FAIL: ")
		if i := strings.Index(name, " "); i > 0 {
			name = name[:i]
		}
		// Subtests report as Parent/Child; the parent is what the ledger names.
		if i := strings.Index(name, "/"); i > 0 {
			failed[name[:i]] = true
		}
		failed[name] = true
	}
	return err == nil, failed, string(out)
}

func TestEveryDecisionIsAssertedOn(t *testing.T) {
	heavy(t, "mutates Go decisions and re-runs their tests, side by side")
	skipIfInner(t)
	// Beside the other slow guards and not after them.
	t.Parallel()

	floor, proofs := repository(t).LogicProofs(t, logicLedger, contractorDir)
	if len(proofs) < floor {
		t.Fatalf(`the logic ledger holds %d entries and its floor is %d.

An entry was removed. Each is the committed proof that a decision is asserted
on rather than merely executed; deleting one makes a red check go away and
takes the proof with it. If a decision genuinely no longer exists, lower the
floor in the same diff so somebody sees it happen.`, len(proofs), floor)
	}

	// The unmutated program must pass first. Proving that a broken version
	// fails means nothing if the working one fails too.
	if ok, _, out := runContractorTests(t, scratchRepo(t)); !ok {
		t.Fatalf("the contractor tests fail before any mutation, so nothing below "+
			"can prove anything:\n%s", lastLines(out, 15))
	}

	for _, m := range proofs {
		t.Run(m.Guard, func(t *testing.T) {
			// Side by side, each on a copy of its own. They used to share
			// one copy, each breaking it and putting it back, which is what
			// made them wait for one another.
			t.Parallel()
			scratch := scratchRepo(t)
			for _, missing := range []struct{ field, value string }{
				{"file", strings.TrimPrefix(m.File, contractorDir+"/")}, {"find", m.Find}, {"replace", m.Replace},
				{"must_fail", m.Test}, {"why", m.Why},
			} {
				if strings.TrimSpace(missing.value) == "" {
					t.Fatalf("this entry has no %s, so it proves nothing", missing.field)
				}
			}

			switch n := scratch.Places(t, m); {
			case n < 0:
				t.Fatalf("the ledger names %s, which is not there.\n\n"+
					"The file moved. Point the entry at where it went; do not delete it.", m.File)
			case n != 1:
				t.Fatalf(`find appears %d times in %s, and an entry that is not exactly
one place is ambiguous.

Widen it until it names one spot. Guessing which of %d it meant is how a ledger
entry ends up proving something other than what it says.`, n, m.File, n)
			}
			scratch.Apply(t, m)

			passed, failed, out := runContractorTests(t, scratch)

			if passed {
				t.Errorf(`the whole suite still passes with this decision broken.

  %s
  in %s, changed to:
      %s

Why that matters:
  %s

Coverage cannot see this: the line still runs, so the number is unchanged and
coverage-exemptions.yml is satisfied. What is missing is an assertion. Write a
test that notices - removing this entry only hides that nothing did.`,
					m.Guard, m.File, strings.TrimSpace(m.Replace), strings.TrimSpace(m.Why))
				return
			}

			if !failed[m.Test] {
				var names []string
				for n := range failed {
					if !strings.Contains(n, "/") {
						names = append(names, n)
					}
				}
				t.Errorf(`the suite failed, but %s did not.

Failing instead: %s

Red for the wrong reason is not proof. A test that dies because a fixture no
longer compiles is just as red as one that caught the defect, and only one of
them is doing its job. Either name the test that actually catches this, or
write one that does.

%s`, m.Test, strings.Join(names, ", "), lastLines(out, 12))
			}
		})
	}
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
