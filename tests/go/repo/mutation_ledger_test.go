package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

// Proof, committed, that each guard fails when the thing it guards is broken.
//
// A test written in the same commit as the change it guards, from the same
// mental model, tends to assert that the change is PRESENT rather than that
// the property HOLDS - and then passes forever whatever the code does. This
// repository has shipped several. The reliable way to tell one from the other
// is to break the thing and watch the test go red, which was done here by hand
// six times in one evening and lost with the terminal.
//
// So the ledger at tests/mutations.yml holds those proofs and this runs them:
// copy the tracked files to a scratch tree, check the guard passes there,
// break the copy, and require the guard to fail for the reason it claims.
//
// Passing before AND failing after is the whole design. A guard that is
// already broken would "fail after" for free, and a guard that dies on a nil
// map is red for the wrong reason - red for the wrong reason is not proof of
// anything, which is why every entry names a substring the failure must
// contain.
//
// See the header of tests/mutations.yml. If one of these fails, the answer is
// almost never to edit the ledger.

type mutation struct {
	Guard    string `yaml:"guard"`
	File     string `yaml:"file"`
	Find     string `yaml:"find"`
	Replace  string `yaml:"replace"`
	Mentions string `yaml:"mentions"`
	Why      string `yaml:"why"`
}

type ledger struct {
	Floor            int        `yaml:"floor"`
	Mutations        []mutation `yaml:"mutations"`
	CommentDependent []struct {
		Test   string `yaml:"test"`
		Reason string `yaml:"reason"`
	} `yaml:"comment_dependent"`
}

func readLedger(t *testing.T) ledger {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "tests", "mutations.yml"))
	if err != nil {
		t.Fatalf("reading tests/mutations.yml: %v", err)
	}
	var l ledger
	if err := yaml.Unmarshal(body, &l); err != nil {
		t.Fatalf("parsing tests/mutations.yml: %v", err)
	}
	return l
}

// The inner runs set repoRootEnv. Everything in this file shells out to the
// test binary, so without this the runner would run itself, recursively,
// once per entry.
func skipIfInner(t *testing.T) {
	t.Helper()
	if os.Getenv(repoRootEnv) != "" {
		t.Skip("inner run: this is the harness, not a guard under test")
	}
}

// scratchRepo copies the tracked files into a temporary directory.
//
// Tracked files only, by way of `git ls-files`: it excludes toolshed/,
// node_modules and .git without needing a list of things to skip, and it is
// the same set a reviewer sees. Anything untracked is by definition not what a
// guard is asserting about the repository.
func scratchRepo(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)

	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Fatalf("listing tracked files: %v", err)
	}
	files := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	if len(files) < 100 {
		t.Fatalf("git ls-files returned %d files, which is not this repository", len(files))
	}

	dir := t.TempDir()
	for _, rel := range files {
		if rel == "" {
			continue
		}
		src := filepath.Join(root, rel)
		info, err := os.Lstat(src)
		if err != nil || !info.Mode().IsRegular() {
			continue // a deleted or non-regular tracked path is not our business
		}
		body, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		dst := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatalf("preparing %s: %v", rel, err)
		}
		if err := os.WriteFile(dst, body, info.Mode().Perm()); err != nil {
			t.Fatalf("writing %s: %v", rel, err)
		}
	}
	return dir
}

var (
	binOnce sync.Once
	binPath string
	binErr  error
)

// One compiled copy of this package's tests, reused for every entry. Compiling
// per entry would dominate the runtime and make the ledger something people
// avoid adding to.
func testBinary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		dir, err := os.MkdirTemp("", "repo-test-bin")
		if err != nil {
			binErr = err
			return
		}
		binPath = filepath.Join(dir, "repo.test")
		cmd := exec.Command("go", "test", "-C", filepath.Join(repoRoot(t), "tests", "go"),
			"-o", binPath, "-c", "./repo/")
		if out, err := cmd.CombinedOutput(); err != nil {
			binErr = err
			binPath = ""
			t.Logf("compiling the repo test package: %v\n%s", err, out)
		}
	})
	if binErr != nil || binPath == "" {
		t.Fatalf("could not compile the repo test package: %v", binErr)
	}
	return binPath
}

// runGuard runs exactly one test against a given tree.
func runGuard(t *testing.T, bin, root, name string) (passed bool, output string) {
	t.Helper()
	cmd := exec.Command(bin, "-test.run", "^"+name+"$", "-test.v")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), repoRootEnv+"="+root)
	out, err := cmd.CombinedOutput()
	text := string(out)
	// A -run pattern matching nothing exits 0 and is not a passing guard; it
	// is a guard that has been renamed out from under the ledger.
	if !strings.Contains(text, "RUN   "+name) {
		return false, "no test named " + name + " ran.\n" + text
	}
	return err == nil, text
}

func TestTheLedgerProvesEachGuardFailsWhenItShould(t *testing.T) {
	skipIfInner(t)

	l := readLedger(t)

	if len(l.Mutations) < l.Floor {
		t.Fatalf("the ledger holds %d entries and its floor is %d.\n\n"+
			"An entry was removed. Each one is the committed proof that a guard "+
			"actually catches something; deleting it makes a red check go away and "+
			"takes the proof with it. If a guard genuinely no longer applies, lower "+
			"the floor in the same diff so somebody sees it happen.",
			len(l.Mutations), l.Floor)
	}

	bin := testBinary(t)
	root := repoRoot(t)

	for _, m := range l.Mutations {
		t.Run(m.Guard, func(t *testing.T) {
			for _, missing := range []struct{ field, value string }{
				{"guard", m.Guard}, {"file", m.File}, {"find", m.Find},
				{"replace", m.Replace}, {"mentions", m.Mentions}, {"why", m.Why},
			} {
				if strings.TrimSpace(missing.value) == "" {
					t.Fatalf("this entry has no %s, so it proves nothing", missing.field)
				}
			}

			// A Go target would prove nothing. The guards are run from a
			// binary compiled once from the real tree, so editing Go source
			// in the scratch copy changes no behaviour and the entry would
			// "fail to fail" for a reason that has nothing to do with the
			// guard. Refuse it rather than let it look like coverage.
			if strings.HasSuffix(m.File, ".go") {
				t.Fatalf("this entry mutates %s, and the ledger cannot judge Go source.\n\n"+
					"The guards run from a binary compiled once from the real tree, so a "+
					"change to Go source in the scratch copy has no effect - the entry "+
					"would pass or fail for reasons unrelated to what it claims.\n\n"+
					"Guards over Go behaviour belong in that program's own package tests, "+
					"beside the code, where a counterexample is an ordinary table entry.",
					m.File)
			}

			original, err := os.ReadFile(filepath.Join(root, m.File))
			if err != nil {
				t.Fatalf("the ledger names %s, which cannot be read: %v\n\n"+
					"The file moved or was deleted. Point the entry at where it went; "+
					"do not delete the entry.", m.File, err)
			}
			if n := strings.Count(string(original), m.Find); n != 1 {
				t.Fatalf("`find` appears %d times in %s, and an entry that is not "+
					"exactly one place is ambiguous.\n\n"+
					"Widen `find` until it names one spot. Guessing which of %d it "+
					"meant is how a ledger entry ends up proving something other than "+
					"what it says.", n, m.File, n)
			}

			// Passing first. A guard that is already broken would satisfy
			// "fails after the mutation" for free, and prove nothing at all.
			scratch := scratchRepo(t)
			if ok, out := runGuard(t, bin, scratch, m.Guard); !ok {
				t.Fatalf("%s already fails on an unmutated copy, so this entry cannot "+
					"prove anything about it.\n\nFix the guard first.\n\n%s", m.Guard, out)
			}

			target := filepath.Join(scratch, m.File)
			body, err := os.ReadFile(target)
			if err != nil {
				t.Fatalf("reading the copy of %s: %v", m.File, err)
			}
			mutated := strings.Replace(string(body), m.Find, m.Replace, 1)
			if mutated == string(body) {
				t.Fatalf("the mutation changed nothing in the copy of %s", m.File)
			}
			if err := os.WriteFile(target, []byte(mutated), 0o644); err != nil {
				t.Fatalf("writing the mutated %s: %v", m.File, err)
			}

			ok, out := runGuard(t, bin, scratch, m.Guard)
			if ok {
				t.Errorf("%s still passes with %s broken.\n\n"+
					"What was broken:\n  %s\n\nWhy that matters:\n  %s\n\n"+
					"This is the change-detector shape: the guard is asserting that "+
					"something is written a certain way rather than that the property "+
					"holds, so it will keep passing however the behaviour changes. Fix "+
					"the guard. Removing this entry only hides that it was never "+
					"checking anything.",
					m.Guard, m.File, strings.TrimSpace(m.Find), strings.TrimSpace(m.Why))
				return
			}
			if !strings.Contains(out, m.Mentions) {
				t.Errorf("%s failed, but not for the reason this entry claims: its "+
					"output never mentions %q.\n\n"+
					"Red for the wrong reason is not proof. A guard that dies on a nil "+
					"map is just as red as one that caught the defect, and only one of "+
					"them is doing its job.\n\n%s", m.Guard, m.Mentions, out)
			}
		})
	}
}

// Nothing may be satisfied by a comment.
//
// A guard that matches a string anywhere in a file can be satisfied by the
// prose explaining the thing rather than by the thing. That has happened twice
// here - most recently a tripwire check satisfied by the comment ABOVE the step
// it was meant to be asserting, so deleting the step passed.
//
// This strips every whole-line comment from the configuration and workflows the
// guards read, and requires every test that passed to still pass. Only
// whole-line comments: a `#` inside a quoted value is not a comment, and
// stripping it would break the file rather than test anything.
func TestNoTestIsSatisfiedByAComment(t *testing.T) {
	skipIfInner(t)

	l := readLedger(t)
	exempt := map[string]string{}
	for _, e := range l.CommentDependent {
		if strings.TrimSpace(e.Reason) == "" {
			t.Errorf("%s is exempt from the comment check with no reason given", e.Test)
		}
		exempt[e.Test] = e.Reason
	}

	bin := testBinary(t)
	before := scratchRepo(t)

	// Materialise the workflows as they will be, then remove the patches: an
	// outstanding patch's context lines include comments, so it would not
	// apply to a comment-stripped file and every workflow guard would fail for
	// that reason instead of the one being tested.
	applyOutstandingPatches(t, before)

	baseline := runAll(t, bin, before)

	after := scratchRepo(t)
	applyOutstandingPatches(t, after)
	stripped := stripComments(t, after)
	if stripped == 0 {
		t.Fatal("no comment was stripped from anything, so this test compared a tree " +
			"with itself and proved nothing")
	}

	for _, name := range satisfiedByProse(baseline, runAll(t, bin, after), exempt) {
		t.Errorf("%s passes with comments and fails without them, so a comment "+
			"was satisfying it rather than the thing it is meant to be "+
			"asserting.\n\n"+
			"Anchor the assertion to structure a comment cannot reach - "+
			"indentation, a parsed key, a resolved path. If it genuinely means to "+
			"read prose, say so in comment_dependent: in tests/mutations.yml with "+
			"a reason.", name)
	}
}

// satisfiedByProse names the tests that flipped from passing to failing when
// the comments went.
//
// Split out from the plumbing so it can be shown saying no as well as yes -
// the same split scripts/attestation makes, for the same reason. A decision
// that only ever runs against the real repository has never been demonstrated
// to reject anything.
//
// Only a pass -> fail flip counts. A test already failing on the unmodified
// tree is failing for some other reason, and reporting it here would blame a
// comment for it.
func satisfiedByProse(baseline, stripped map[string]bool, exempt map[string]string) []string {
	var out []string
	for name, passedBefore := range baseline {
		if !passedBefore {
			continue
		}
		passedAfter, ran := stripped[name]
		if !ran || passedAfter {
			continue
		}
		if _, ok := exempt[name]; ok {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// runAll returns each test's verdict, by name.
func runAll(t *testing.T, bin, root string) map[string]bool {
	t.Helper()
	cmd := exec.Command(bin, "-test.v")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), repoRootEnv+"="+root)
	out, _ := cmd.CombinedOutput()

	verdicts := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "--- PASS: "):
			verdicts[field(line, "--- PASS: ")] = true
		case strings.HasPrefix(line, "--- FAIL: "):
			verdicts[field(line, "--- FAIL: ")] = false
		}
	}
	if len(verdicts) == 0 {
		t.Fatalf("no test verdict was parsed from the run in %s:\n%s", root, out)
	}
	return verdicts
}

func field(line, prefix string) string {
	rest := strings.TrimPrefix(line, prefix)
	if i := strings.Index(rest, " "); i > 0 {
		return rest[:i]
	}
	return rest
}

// applyOutstandingPatches applies each patch in the scratch tree and removes
// it, so the tree is the one that will exist after the hand-over.
func applyOutstandingPatches(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, ".github", "patches")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".patch") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		cmd := exec.Command("git", "apply", path)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("applying %s in the scratch tree: %v\n%s", e.Name(), err, out)
		}
		if err := os.Remove(path); err != nil {
			t.Fatalf("removing the applied %s: %v", e.Name(), err)
		}
	}
}

// stripComments removes whole-line `#` comments from the files the guards read,
// and reports how many lines went.
func stripComments(t *testing.T, root string) int {
	t.Helper()

	var targets []string
	for _, dir := range []string{filepath.Join(".github", "workflows"), "scripts"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if ext := filepath.Ext(p); ext == ".yml" || ext == ".yaml" {
				targets = append(targets, p)
			}
			return nil
		})
	}
	targets = append(targets,
		filepath.Join(root, ".pre-commit-config.yaml"),
		filepath.Join(root, "taskfile.yml"),
	)

	removed := 0
	for _, p := range targets {
		body, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var kept []string
		for _, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				removed++
				continue
			}
			kept = append(kept, line)
		}
		if err := os.WriteFile(p, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
			t.Fatalf("writing the comment-stripped %s: %v", p, err)
		}
	}
	return removed
}

// The comment check's decision, shown saying no as well as yes.
//
// Running it only against the real repository demonstrates that it does not
// fire today, which is not the same as demonstrating it would.
func TestSatisfiedByProseOnlyNamesAPassToFailFlip(t *testing.T) {
	cases := []struct {
		name     string
		baseline map[string]bool
		stripped map[string]bool
		exempt   map[string]string
		want     []string
	}{
		{
			name:     "a test satisfied by a comment flips and is named",
			baseline: map[string]bool{"TestReadsProse": true},
			stripped: map[string]bool{"TestReadsProse": false},
			want:     []string{"TestReadsProse"},
		},
		{
			name:     "a test that was already failing is not blamed on a comment",
			baseline: map[string]bool{"TestBroken": false},
			stripped: map[string]bool{"TestBroken": false},
			want:     nil,
		},
		{
			name:     "a test that passes either way is not named",
			baseline: map[string]bool{"TestAnchored": true},
			stripped: map[string]bool{"TestAnchored": true},
			want:     nil,
		},
		{
			name:     "an exempt test is not named",
			baseline: map[string]bool{"TestReadsProse": true},
			stripped: map[string]bool{"TestReadsProse": false},
			exempt:   map[string]string{"TestReadsProse": "it asserts a documented reason"},
			want:     nil,
		},
		{
			name:     "a test that did not run in the second pass is not named",
			baseline: map[string]bool{"TestVanished": true},
			stripped: map[string]bool{},
			want:     nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := satisfiedByProse(c.baseline, c.stripped, c.exempt)
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("got %v, want %v", got, c.want)
				}
			}
		})
	}
}
