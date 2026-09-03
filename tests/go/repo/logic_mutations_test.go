package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

type logicLedger struct {
	Floor int `yaml:"floor"`
	Logic []struct {
		Decision string `yaml:"decision"`
		File     string `yaml:"file"`
		Find     string `yaml:"find"`
		Replace  string `yaml:"replace"`
		MustFail string `yaml:"must_fail"`
		Why      string `yaml:"why"`
	} `yaml:"logic"`
}

// scratchContractor gives a full copy of the repository with the program in
// it, and the directory to run its tests from.
//
// The whole repository rather than just scripts/contractor, because the
// program's own contract tests find the repository root by walking up from
// their source file and read management/cluster and CLAUDE.md from there. A
// bare copy of the program makes six of them fail for a reason that has
// nothing to do with the mutation - which the first version of this did, and
// which would have made every entry below "fail" without proving anything.
//
// Copied once and restored between entries rather than re-copied, because the
// only thing that changes is one file.
func scratchContractor(t *testing.T) string {
	t.Helper()
	return filepath.Join(scratchRepo(t), "scripts", "contractor")
}

// runContractorTests reports whether the suite passed, and which tests failed.
func runContractorTests(t *testing.T, dir string) (passed bool, failed map[string]bool, output string) {
	t.Helper()
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()

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
	skipIfInner(t)
	root := repoRoot(t)

	body, err := os.ReadFile(filepath.Join(root, "tests", "logic-mutations.yml"))
	if err != nil {
		t.Fatalf("reading tests/logic-mutations.yml: %v", err)
	}
	var l logicLedger
	if err := yaml.Unmarshal(body, &l); err != nil {
		t.Fatalf("parsing tests/logic-mutations.yml: %v", err)
	}

	if len(l.Logic) < l.Floor {
		t.Fatalf(`the logic ledger holds %d entries and its floor is %d.

An entry was removed. Each is the committed proof that a decision is asserted
on rather than merely executed; deleting one makes a red check go away and
takes the proof with it. If a decision genuinely no longer exists, lower the
floor in the same diff so somebody sees it happen.`, len(l.Logic), l.Floor)
	}

	// The unmutated program must pass first. Proving that a broken version
	// fails means nothing if the working one fails too.
	dir := scratchContractor(t)
	if ok, _, out := runContractorTests(t, dir); !ok {
		t.Fatalf("the contractor tests fail before any mutation, so nothing below "+
			"can prove anything:\n%s", lastLines(out, 15))
	}

	for _, m := range l.Logic {
		t.Run(m.Decision, func(t *testing.T) {
			for _, missing := range []struct{ field, value string }{
				{"file", m.File}, {"find", m.Find}, {"replace", m.Replace},
				{"must_fail", m.MustFail}, {"why", m.Why},
			} {
				if strings.TrimSpace(missing.value) == "" {
					t.Fatalf("this entry has no %s, so it proves nothing", missing.field)
				}
			}

			target := filepath.Join(dir, m.File)
			src, err := os.ReadFile(target)
			if err != nil {
				t.Fatalf("the ledger names %s, which cannot be read: %v\n\n"+
					"The file moved. Point the entry at where it went; do not delete it.",
					m.File, err)
			}
			if n := strings.Count(string(src), m.Find); n != 1 {
				t.Fatalf(`find appears %d times in %s, and an entry that is not exactly
one place is ambiguous.

Widen it until it names one spot. Guessing which of %d it meant is how a ledger
entry ends up proving something other than what it says.`, n, m.File, n)
			}

			mutated := strings.Replace(string(src), m.Find, m.Replace, 1)
			if err := os.WriteFile(target, []byte(mutated), 0o644); err != nil {
				t.Fatalf("writing the mutated %s: %v", m.File, err)
			}
			// Put it back whatever happens, so the next entry starts from a
			// working program rather than from this one's damage.
			defer func() { _ = os.WriteFile(target, src, 0o644) }()

			passed, failed, out := runContractorTests(t, dir)

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
					m.Decision, m.File, strings.TrimSpace(m.Replace), strings.TrimSpace(m.Why))
				return
			}

			if !failed[m.MustFail] {
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

%s`, m.MustFail, strings.Join(names, ", "), lastLines(out, 12))
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
