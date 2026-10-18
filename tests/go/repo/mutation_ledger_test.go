package repo

import (
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"

	"homelab/details/applications"
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
	Guard string `yaml:"guard"`
	File  string `yaml:"file"`
	// Test is, in an application's own ledger, which of the application's
	// tests must be the one that failed: the guard there is the one that
	// runs them all.
	Test     string `yaml:"test"`
	Find     string `yaml:"find"`
	Replace  string `yaml:"replace"`
	Create   string `yaml:"create"`
	Mentions string `yaml:"mentions"`
	// Rule is, for a proof of the code or the configuration rules, the rule
	// that must be among those refusing: a Semgrep rule's id, or the package
	// of a Conftest rule under rules. Those harnesses run every rule at once,
	// so a failure that mentions the right words could still be another
	// rule's (#780).
	Rule string `yaml:"rule"`
	Why  string `yaml:"why"`
	// ReadsSource opts a Go target in, for the one kind of guard the blanket
	// refusal below gets wrong. See the refusal for why the default stays no.
	ReadsSource bool `yaml:"reads_source"`
	// Source is where the proof is written: its file in the estate's ledger,
	// or its place in an application's. It decides which part of the meta
	// lane proves it.
	Source string `yaml:"-"`
}

// creates reports whether the entry proves a guard by adding a file rather
// than by editing one.
//
// Some guards only have anything to say when something is present - the
// outstanding-patch check is the example, and it skips entirely when no patch
// is outstanding, which is the normal state. A substitution cannot reach that:
// there is nothing in the tree to substitute. So an entry may instead plant
// the thing the guard exists to catch.
func (m mutation) creates() bool { return strings.TrimSpace(m.Create) != "" }

type ledger struct {
	Floor            int        `yaml:"floor"`
	Mutations        []mutation `yaml:"mutations"`
	CommentDependent []struct {
		Test   string `yaml:"test"`
		Reason string `yaml:"reason"`
	} `yaml:"comment_dependent"`
}

// applicationLedger is where an application keeps the proofs that are about
// it, inside its own directory.
const applicationLedger = applicationTestsDir + "/mutations.yml"

// applicationMutations reads every application's own ledger.
//
// An application's proofs are kept with the application, so that removing it
// removes them (#590): an entry in tests/mutations.yml that broke one of its
// files would be a line elsewhere that has to go too. Each ledger has its own
// floor, and every entry in it must break a file of that application's own -
// a proof about the estate that happened to live in an application's
// directory would vanish with the application and take the proof with it.
func applicationMutations(t *testing.T) []mutation {
	t.Helper()
	root := repoRoot(t)
	apps, err := applications.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []mutation
	for _, a := range apps {
		rel := a.Root + "/" + applicationLedger
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if os.IsNotExist(err) {
			continue // an application with no proofs of its own
		}
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		var l ledger
		if err := yaml.Unmarshal(body, &l); err != nil {
			t.Fatalf("parsing %s: %v", rel, err)
		}
		if len(l.Mutations) < l.Floor {
			t.Fatalf("%s holds %d entries and its floor is %d. An entry was removed; "+
				"if a proof genuinely no longer applies, lower the floor in the same diff "+
				"so somebody sees it happen.", rel, len(l.Mutations), l.Floor)
		}
		for i, m := range l.Mutations {
			m.Source = fmt.Sprintf("%s#%d", rel, i)
			if !strings.HasPrefix(m.File, a.Root+"/") {
				t.Fatalf("%s has an entry that breaks %s, which is not this application's. "+
					"A proof about the estate belongs in tests/mutations.yml, where removing "+
					"an application does not remove it.", rel, m.File)
			}
			if m.Test != "" && m.Guard != "TestEveryApplicationsOwnTestsPass" {
				t.Fatalf("%s names the application's own test %s under the guard %s, which does not run it", rel, m.Test, m.Guard)
			}
			out = append(out, m)
		}
	}
	return out
}

// ledgerEntries is where the estate's proofs are kept, one to a file.
//
// One to a file because they were one list in one file, with a count of them
// at its head, and every pull request that added a guard appended to the
// same place and raised the same number: any two of them conflicted, and a
// conflict here costs a new branch and a new pull request. A file of its own
// is touched by nobody else. What the count was for - that a proof is not
// removed quietly - is the directory's entry in .github/sensitive-paths: a
// deleted proof is a deleted file, and that opens a review.
const ledgerEntries = "tests/mutations"

// isLedger reports whether a path is the ledger or one of its proofs. Several
// guards leave it out of what they read: a proof has to hold the broken form
// of the thing its guard refuses.
func isLedger(rel string) bool {
	rel = filepath.ToSlash(rel)
	return rel == ledgerEntries+".yml" || strings.HasPrefix(rel, ledgerEntries+"/")
}

func readLedger(t *testing.T) ledger {
	t.Helper()
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "tests", "mutations.yml"))
	if err != nil {
		t.Fatalf("reading tests/mutations.yml: %v", err)
	}
	var l ledger
	if err := yaml.Unmarshal(body, &l); err != nil {
		t.Fatalf("parsing tests/mutations.yml: %v", err)
	}
	if len(l.Mutations) > 0 {
		t.Fatalf("tests/mutations.yml holds %d proof(s) under mutations:. Each is a file of its own under %s, named for its guard, so that two changes adding one never meet.", len(l.Mutations), ledgerEntries)
	}
	files, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(ledgerEntries), "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		var m mutation
		if err := yaml.Unmarshal(raw, &m); err != nil {
			t.Fatalf("parsing %s/%s: %v", ledgerEntries, filepath.Base(file), err)
		}
		if m.Guard == "" || !strings.HasPrefix(filepath.Base(file), m.Guard+".") {
			t.Fatalf("%s/%s proves the guard %q and is not named for it. A proof is found by its guard's name, so name the file <guard>.<anything>.yml.", ledgerEntries, filepath.Base(file), m.Guard)
		}
		m.Source = filepath.Base(file)
		l.Mutations = append(l.Mutations, m)
	}
	if len(l.Mutations) == 0 {
		t.Fatalf("%s holds no proof at all, so nothing shows any guard fails when it should", ledgerEntries)
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

	// And then the copy becomes a git repository, so that the guards being
	// proved can enumerate it the same way they enumerate the real tree.
	//
	// This is what lets trackedFiles be the single enumeration primitive. Before
	// it, `git ls-files` here exited 128 - "not a git repository" - so guards
	// that needed to run under the ledger had to walk from the root with a
	// hand-written skip list instead, and that list was the stale artifact the
	// walk was supposed to avoid.
	//
	// After the patches, not before: a patch may add or remove files, and the
	// index has to describe the tree the guards are actually judged against.
	//
	// No commit and no user identity are needed - `git ls-files` reads the
	// index.
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init in the scratch tree: %v\n%s", err, out)
	}
	indexScratchTree(t, dir)
	return dir
}

// indexScratchTree makes `git ls-files` in the scratch tree describe the tree
// as it is right now.
//
// It has to run again after a mutation, not only after the copy. An entry that
// PLANTS a file writes something the index has never seen, so a guard
// enumerating tracked files would not find it and would pass - reporting that
// the planted violation is fine. That is the precise failure the ledger exists
// to catch, and it caught it here: TestEveryHelmReleaseSaysWhereItsPodSpecLives
// went green against an undescribed chart the moment its enumeration changed.
//
// -f because a tracked file is tracked even if .gitignore matches it, and
// dropping one would quietly shrink the universe under test.
func indexScratchTree(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "add", "-A", "-f")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("indexing the scratch tree: %v\n%s", err, out)
	}
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
	heavy(t, "a check on the checks: it copies the repository and runs a guard per mutation, side by side")

	part := readMetaPartOrFail(t)
	if !part.provesAny() {
		t.Skipf("this is part %d of %d of the meta lane, which is the comment test's; the ledger's proofs are in the others", part.index, part.total)
	}

	l := readLedger(t)

	var entries []mutation
	for _, m := range append(append([]mutation{}, l.Mutations...), applicationMutations(t)...) {
		if part.proves(m.Source) {
			entries = append(entries, m)
		}
	}
	if len(entries) == 0 {
		t.Fatalf("part %d of %d of the meta lane holds no proof, so it proves nothing; the ledger was read as %d proofs", part.index, part.total, len(l.Mutations))
	}

	bin := testBinary(t)

	// Passing first, once for each guard. Every proof's copy is the same
	// unbroken tree until its mutation lands, so a guard's answer on it is
	// one answer, and asking it once per proof was asking it again: 168 of
	// the ledger's 644 guard runs, 135 of them a whole Conftest or Semgrep
	// scan, and the meta lane past its ten minutes (#779). The first proof
	// of a guard asks, in its own copy, and the rest wait for that answer.
	var mu sync.Mutex
	unbroken := map[string]*unbrokenRun{}
	unbrokenOf := func(guard string) *unbrokenRun {
		mu.Lock()
		defer mu.Unlock()
		if unbroken[guard] == nil {
			unbroken[guard] = &unbrokenRun{}
		}
		return unbroken[guard]
	}

	for _, m := range entries {
		name := m.Guard
		if m.Test != "" {
			name += "/" + m.Test
		}
		t.Run(name, func(t *testing.T) {
			// Side by side. Each proof has a copy of the repository of its
			// own and runs its guard in a process of its own, so nothing one
			// does can be seen by another - and one after another, two
			// hundred of them were four minutes of every pull request.
			t.Parallel()
			// A copy of the tree to break. The entry's target is read from
			// the copy, so what is counted and what is broken are one file.
			scratch := scratchRepo(t)
			if problems := placeProblems(scratch, m); len(problems) > 0 {
				t.Fatal(problems[0])
			}

			// Passing first. A guard that is already broken would satisfy
			// "fails after the mutation" for free, and prove nothing at all.
			u := unbrokenOf(m.Guard)
			u.once.Do(func() { u.passed, u.output = runGuard(t, bin, scratch, m.Guard) })
			if !u.passed {
				t.Fatalf("%s already fails on an unmutated copy, so this entry cannot "+
					"prove anything about it.\n\nFix the guard first.\n\n%s", m.Guard, u.output)
			}

			target := filepath.Join(scratch, m.File)
			if m.creates() {
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					t.Fatalf("preparing %s: %v", m.File, err)
				}
				if err := os.WriteFile(target, []byte(m.Create), 0o644); err != nil {
					t.Fatalf("planting %s: %v", m.File, err)
				}
			} else {
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
			}
			indexScratchTree(t, scratch)

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
			if m.Rule != "" && !refusedBy(out, m.Guard, m.Rule) {
				t.Errorf("%s failed, but %s is not among the rules that refused, and this entry "+
					"names it.\n\nA proof of one rule that another satisfies shows nothing about "+
					"the one it names.\n\n%s", m.Guard, m.Rule, out)
			}
			if !strings.Contains(out, m.Mentions) {
				t.Errorf("%s failed, but not for the reason this entry claims: its "+
					"output never mentions %q.\n\n"+
					"Red for the wrong reason is not proof. A guard that dies on a nil "+
					"map is just as red as one that caught the defect, and only one of "+
					"them is doing its job.\n\n%s", m.Guard, m.Mentions, out)
			}
			// An application's own test, run by the guard that runs them
			// all: the one named has to be the one that failed.
			if m.Test != "" && !strings.Contains(out, "--- FAIL: "+m.Test) {
				t.Errorf("%s failed with %s broken, but %s is not the test that failed.\n\n%s", m.Guard, m.File, m.Test, out)
			}
		})
	}
}

// The harnesses that run many rules at once, and where each keeps them.
const (
	codeRulesHarness          = "TestTheCodeRulesHold"
	configurationRulesHarness = "TestTheConfigurationRulesHold"
)

// ruleHarness is where the rules a guard runs are kept, or "" when the guard
// runs none.
func ruleHarness(guard string) string {
	switch guard {
	case codeRulesHarness:
		return semgrepRules
	case configurationRulesHarness:
		return conftestRules
	}
	return ""
}

// rulesDeclaredIn is every rule a tree declares in one harness's directory,
// by the name a proof gives it: a Semgrep rule's id, or a Conftest rule's
// package under rules.
func rulesDeclaredIn(root, dir string) (map[string]bool, error) {
	declared := map[string]bool{}
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
	if err != nil {
		return nil, fmt.Errorf("%s cannot be read, so no proof's rule can be found there: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue // the examples, which declare no rule
		}
		file := e.Name()
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(dir), file))
		if err != nil {
			return nil, fmt.Errorf("reading %s/%s: %v", dir, file, err)
		}
		switch {
		case strings.HasSuffix(file, ".yml"):
			var doc struct {
				Rules []struct {
					ID string `yaml:"id"`
				} `yaml:"rules"`
			}
			if err := yaml.Unmarshal(body, &doc); err != nil {
				return nil, fmt.Errorf("%s is not a rule file that can be read: %v", file, err)
			}
			for _, r := range doc.Rules {
				declared[r.ID] = true
			}
		case strings.HasSuffix(file, ".rego") && !strings.HasSuffix(file, "_test.rego"):
			if m := packageClause.FindStringSubmatch(string(body)); m != nil {
				if name, ok := strings.CutPrefix(m[1], "rules."); ok {
					declared[name] = true
				}
			}
		}
	}
	if len(declared) == 0 {
		return nil, fmt.Errorf("no rule was read from %s, so no proof's rule can be found there", dir)
	}
	return declared, nil
}

// refusedBy reports whether a rule harness's report says the named rule
// refused. Each harness writes a refusal with the rule's name in a place of
// its own: the code rules as `<file>:<line>: <id>: <message>`, or as
// `<id> found nothing` for a rule whose finds are expected; the configuration
// rules as Conftest's `FAIL - <file> - main - <package>: <message>`.
func refusedBy(out, guard, rule string) bool {
	r := regexp.QuoteMeta(rule)
	var forms []string
	switch guard {
	case codeRulesHarness:
		forms = []string{`(?m)^\s+\S+:\d+: ` + r + `: `, `(?m)^\s+\S+:\d+: ` + r + ` found nothing, `}
	case configurationRulesHarness:
		forms = []string{`(?m)^\s*FAIL - .* - main - ` + r + `: `}
	}
	for _, f := range forms {
		if regexp.MustCompile(f).MatchString(out) {
			return true
		}
	}
	return false
}

func TestRefusedByReadsTheRuleFromEachHarnesssReport(t *testing.T) {
	semgrep := "    code_rules_test.go:169: a code rule refuses the repository:\n\n" +
		"          taskfile.yml:130: no-workflow-or-task-picks-a-site-by-name: names a site as a value.\n" +
		"    code_rules_test.go:154: the-teardown-resolves-the-bucket found nothing, and teardown.go does not\n"
	conftest := "    configuration_rules_test.go:101: a configuration rule refuses the repository:\n\n" +
		"          FAIL - Combined - main - suppliers: clusters/x.yaml: chart source is not an approved supplier\n"
	for _, c := range []struct {
		out, guard, rule string
		want             bool
	}{
		{semgrep, codeRulesHarness, "no-workflow-or-task-picks-a-site-by-name", true},
		{semgrep, codeRulesHarness, "the-teardown-resolves-the-bucket", true},
		{semgrep, codeRulesHarness, "no-program-picks-a-site-by-name", false},
		{semgrep, codeRulesHarness, "site", false},
		{conftest, configurationRulesHarness, "suppliers", true},
		{conftest, configurationRulesHarness, "flux_source_secret", false},
		{conftest, configurationRulesHarness, "approved", false},
		{conftest, codeRulesHarness, "suppliers", false},
		{semgrep, "TestSomethingElse", "no-workflow-or-task-picks-a-site-by-name", false},
	} {
		if got := refusedBy(c.out, c.guard, c.rule); got != c.want {
			t.Errorf("refusedBy(%s, %q) = %v, want %v", c.guard, c.rule, got, c.want)
		}
	}
}

// metaPartEnv says which of the meta lane's jobs this is, as "<index>/<total>"
// - GitHub's own count of the jobs it made from one definition, so no number
// is written down that could stop matching how many there are. Part 0 runs
// the comment test; parts 1 to total-1 share the ledger's proofs. Unset, as at
// a terminal or on the nightly run, one run is the whole of both.
const metaPartEnv = "META_PART"

// metaPart is which part of the meta lane a run is. The zero value is the
// whole of it.
type metaPart struct{ index, total int }

func readMetaPart(value string) (metaPart, error) {
	if value == "" {
		return metaPart{}, nil
	}
	i, n, ok := strings.Cut(value, "/")
	index, errI := strconv.Atoi(i)
	total, errN := strconv.Atoi(n)
	if !ok || errI != nil || errN != nil || total < 2 || index < 0 || index >= total {
		return metaPart{}, fmt.Errorf("%s is %q, which is not a part of the meta lane: it is <index>/<total>, with at least two parts, part 0 the comment test's", metaPartEnv, value)
	}
	return metaPart{index, total}, nil
}

// readMetaPartOrFail is this run's part. A part it cannot read fails rather
// than running everything or nothing: either would say the ledger was proved.
func readMetaPartOrFail(t *testing.T) metaPart {
	t.Helper()
	part, err := readMetaPart(os.Getenv(metaPartEnv))
	if err != nil {
		t.Fatal(err)
	}
	return part
}

func (p metaPart) whole() bool { return p.total == 0 }

func (p metaPart) runsComments() bool { return p.whole() || p.index == 0 }

func (p metaPart) provesAny() bool { return p.whole() || p.index > 0 }

// proves reports whether this part proves the proof written at source. Each
// proof falls in exactly one of parts 1 to total-1, by a hash of where it is
// written, so a proof added or removed moves no other.
func (p metaPart) proves(source string) bool {
	if p.whole() {
		return true
	}
	if p.index == 0 {
		return false
	}
	h := fnv.New32a()
	h.Write([]byte(source))
	return int(h.Sum32()%uint32(p.total-1)) == p.index-1
}

func TestReadMetaPartTakesOnlyAPartItCanPlace(t *testing.T) {
	for _, c := range []struct {
		value string
		want  metaPart
		ok    bool
	}{
		{"", metaPart{}, true},
		{"0/17", metaPart{0, 17}, true},
		{"16/17", metaPart{16, 17}, true},
		{"17/17", metaPart{}, false},
		{"0/1", metaPart{}, false},
		{"-1/3", metaPart{}, false},
		{"3", metaPart{}, false},
		{"a/b", metaPart{}, false},
	} {
		got, err := readMetaPart(c.value)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("readMetaPart(%q) = %v, %v", c.value, got, err)
		}
	}
}

func TestEveryProofFallsInExactlyOnePartOfTheLedger(t *testing.T) {
	sources := []string{"TestA.1.yml", "TestA.2.yml", "TestB.x.yml", "modules/applications/a/tests/mutations.yml#0", "", "TestC.long-name-of-a-proof.yml"}
	for _, total := range []int{2, 5, 17} {
		for _, s := range sources {
			if (metaPart{0, total}).proves(s) {
				t.Errorf("part 0 of %d, the comment test's, proves %q", total, s)
			}
			held := 0
			for i := 1; i < total; i++ {
				if (metaPart{i, total}).proves(s) {
					held++
				}
			}
			if held != 1 {
				t.Errorf("%q falls in %d of the %d ledger parts, not one", s, held, total-1)
			}
		}
	}
	if !(metaPart{}).proves("TestA.1.yml") || !(metaPart{}).runsComments() {
		t.Error("the whole of the meta lane does not run everything")
	}
}

// unbrokenRun is a guard's answer on the unbroken tree, asked once.
type unbrokenRun struct {
	once   sync.Once
	passed bool
	output string
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
	heavy(t, "a check on the checks: it re-runs the whole package with comments stripped, about a minute")
	if part := readMetaPartOrFail(t); !part.runsComments() {
		t.Skipf("this is part %d of %d of the meta lane, which proves a share of the ledger; the comment test is part 0", part.index, part.total)
	}

	l := readLedger(t)
	exempt := map[string]string{}
	for _, e := range l.CommentDependent {
		if strings.TrimSpace(e.Reason) == "" {
			t.Errorf("%s is exempt from the comment check with no reason given", e.Test)
		}
		exempt[e.Test] = e.Reason
	}

	bin := testBinary(t)
	// scratchRepo materialises the tree as it will be and removes the
	// patches, which matters twice over here: an outstanding patch's context
	// lines include comments, so it would not apply to a comment-stripped file
	// and every workflow guard would fail for that reason instead of the one
	// being tested.
	before := scratchRepo(t)
	baseline := runAll(t, bin, before)

	after := scratchRepo(t)
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
	verdicts, out := allVerdicts(bin, root)
	if len(verdicts) == 0 {
		t.Fatalf("no test verdict was parsed from the run in %s:\n%s", root, out)
	}
	return verdicts
}

// allVerdicts is runAll with nothing of the test in it, so that two runs
// can be made side by side: a test may only be failed from its own
// goroutine.
func allVerdicts(bin, root string) (map[string]bool, []byte) {
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
	return verdicts, out
}

// outputOf is what one test said in a verbose run of many. Tests that run
// beside each other are interleaved, and Go says whose each stretch is with a
// line of its own - === RUN, CONT, NAME or PAUSE, and the verdict - so a line
// belongs to the test the last such line named, or to one of its subtests.
func outputOf(out []byte, name string) string {
	var said []string
	current := ""
	for _, line := range strings.Split(string(out), "\n") {
		if named, ok := testNamedBy(strings.TrimSpace(line)); ok {
			current = named
		}
		if current == name || strings.HasPrefix(current, name+"/") {
			said = append(said, line)
		}
	}
	return strings.Join(said, "\n")
}

// testNamedBy is the test a line of Go's verbose output hands the stretch
// after it to, if it is such a line.
func testNamedBy(line string) (string, bool) {
	for _, marker := range []string{"=== RUN", "=== CONT", "=== NAME", "=== PAUSE", "--- FAIL:", "--- PASS:", "--- SKIP:"} {
		if rest, ok := strings.CutPrefix(line, marker); ok {
			name, _, _ := strings.Cut(strings.TrimSpace(rest), " ")
			return name, name != ""
		}
	}
	return "", false
}

func TestOutputOfKeepsOneTestsLinesFromAnInterleavedRun(t *testing.T) {
	out := []byte(strings.Join([]string{
		"=== RUN   TestA",
		"=== PAUSE TestA",
		"=== RUN   TestB",
		"    b_test.go:1: from B",
		"=== CONT  TestA",
		"    a_test.go:1: from A",
		"=== RUN   TestA/sub",
		"    a_test.go:2: from A's subtest",
		"=== NAME  TestB",
		"    b_test.go:2: from B again",
		"--- FAIL: TestA (0.01s)",
		"    --- FAIL: TestA/sub (0.00s)",
		"--- PASS: TestB (0.02s)",
		"FAIL",
	}, "\n"))
	got := outputOf(out, "TestA")
	for _, want := range []string{"from A", "from A's subtest", "--- FAIL: TestA (0.01s)", "--- FAIL: TestA/sub"} {
		if !strings.Contains(got, want) {
			t.Errorf("TestA's lines lack %q:\n%s", want, got)
		}
	}
	for _, not := range []string{"from B", "--- PASS: TestB"} {
		if strings.Contains(got, not) {
			t.Errorf("TestA's lines hold TestB's %q:\n%s", not, got)
		}
	}
	if outputOf(out, "TestC") != "" {
		t.Error("a test that never ran said something")
	}
}

func field(line, prefix string) string {
	rest := strings.TrimPrefix(line, prefix)
	if i := strings.Index(rest, " "); i > 0 {
		return rest[:i]
	}
	return rest
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

// placeProblems is everything wrong with a ledger entry that can be told
// without running its guard: a field it lacks, Go source it may not judge, a
// file that is not where it says or already is where it would plant one, and
// a `find` that is not exactly one place. The ledger refuses an entry for any
// of these before it runs anything, and TestEveryProofStillFindsItsPlace asks
// the same of every entry at push, so a proof left behind by a change is found
// in milliseconds rather than eight minutes into the meta lane.
func placeProblems(root string, m mutation) []string {
	required := []struct{ field, value string }{
		{"guard", m.Guard}, {"file", m.File},
		{"mentions", m.Mentions}, {"why", m.Why},
	}
	if m.creates() {
		if m.Find != "" || m.Replace != "" {
			return []string{"this entry both creates a file and substitutes in one; " +
				"it can only prove one thing, so say which"}
		}
	} else {
		required = append(required,
			struct{ field, value string }{"find", m.Find},
			struct{ field, value string }{"replace", m.Replace})
	}
	harness := ruleHarness(m.Guard)
	if harness != "" {
		required = append(required, struct{ field, value string }{"rule", m.Rule})
	} else if m.Rule != "" {
		return []string{fmt.Sprintf("this entry names the rule %s, and %s runs no rules; only a proof of %s or %s names one",
			m.Rule, m.Guard, codeRulesHarness, configurationRulesHarness)}
	}
	for _, missing := range required {
		if strings.TrimSpace(missing.value) == "" {
			return []string{fmt.Sprintf("this entry has no %s, so it proves nothing", missing.field)}
		}
	}

	// A Go target usually proves nothing. The guards are run from a
	// binary compiled once from the real tree, so editing Go source
	// in the scratch copy changes no behaviour and the entry would
	// "fail to fail" for a reason that has nothing to do with the
	// guard. Refuse it rather than let it look like coverage.
	//
	// EXCEPT WHERE THE GUARD READS THE SOURCE AS DATA, which this
	// refusal used to state as a property of the ledger when it is
	// really a property of the guard. runGuard sets the repo-root
	// environment variable to the scratch tree and repoRoot honours
	// it, so a guard that opens .go files and inspects their text sees
	// the mutation in full. Only guards asserting COMPILED BEHAVIOUR
	// are blind to it.
	//
	// So the opt-in is explicit and narrow rather than the default
	// being loosened: `reads_source: true` says "this guard reads the
	// named file, it does not execute it". Getting that wrong makes an
	// entry that cannot fail, which is why it has to be claimed rather
	// than inferred.
	if strings.HasSuffix(m.File, ".go") && !m.ReadsSource {
		return []string{fmt.Sprintf("this entry mutates %s, and the ledger cannot judge Go source.\n\n"+
			"The guards run from a binary compiled once from the real tree, so a "+
			"change to Go source in the scratch copy has no effect - the entry "+
			"would pass or fail for reasons unrelated to what it claims.\n\n"+
			"Guards over Go behaviour belong in that program's own package tests, "+
			"beside the code, where a counterexample is an ordinary table entry.\n\n"+
			"If this guard READS the file as data rather than executing it, say so "+
			"with `reads_source: true` - the scratch tree does reach a guard that "+
			"opens source and inspects its text.",
			m.File)}
	}

	if harness != "" {
		declared, err := rulesDeclaredIn(root, harness)
		if err != nil {
			return []string{err.Error()}
		}
		if !declared[m.Rule] {
			return []string{fmt.Sprintf("this entry names the rule %s, which %s does not declare. "+
				"A rule renamed or retired leaves its proofs naming nothing: point each at the rule that refuses "+
				"what it breaks.", m.Rule, harness)}
		}
	}

	if m.creates() {
		if _, err := os.Stat(filepath.Join(root, m.File)); err == nil {
			return []string{fmt.Sprintf("this entry plants %s, and that file already exists in the "+
				"repository. Planting it would overwrite something real, and the "+
				"guard would be judged against the wrong thing.", m.File)}
		}
		return nil
	}
	original, err := os.ReadFile(filepath.Join(root, m.File))
	if err != nil {
		return []string{fmt.Sprintf("the ledger names %s, which cannot be read: %v\n\n"+
			"The file moved or was deleted. Point the entry at where it went; "+
			"do not delete the entry.", m.File, err)}
	}
	if n := strings.Count(string(original), m.Find); n != 1 {
		return []string{fmt.Sprintf("`find` appears %d times in %s, and an entry that is not "+
			"exactly one place is ambiguous.\n\n"+
			"Widen `find` until it names one spot. Guessing which of %d it "+
			"meant is how a ledger entry ends up proving something other than "+
			"what it says.", n, m.File, n)}
	}
	return nil
}

// Every proof still finds its place, checked at push. A change that moves,
// deletes or rewrites what a proof breaks leaves the proof pointing at
// nothing, and the ledger only says so when it runs - on the pull request,
// minutes in. Nothing here runs a guard: it reads each entry and the file it
// names, which is the part of the ledger's judgement that costs nothing.
func TestEveryProofStillFindsItsPlace(t *testing.T) {
	root := repoRoot(t)
	entries := append(append([]mutation{}, readLedger(t).Mutations...), applicationMutations(t)...)
	if len(entries) < 100 {
		t.Fatalf("only %d proofs were read, which is not this ledger - the reader has stopped matching", len(entries))
	}
	for _, m := range entries {
		for _, p := range placeProblems(root, m) {
			name := m.Guard
			if m.Test != "" {
				name += "/" + m.Test
			}
			t.Errorf("%s (%s): %s", name, m.File, p)
		}
	}
}
