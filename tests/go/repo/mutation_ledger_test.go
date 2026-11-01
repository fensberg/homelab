package repo

import (
	"flag"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"homelab/tests/parsed"
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

// mutation is a ledger's proof, as parsed reads it: only a proof read from a
// ledger may be placed in a file or applied to a copy.
type mutation = parsed.Proof

type ledger struct {
	Floor     int        `yaml:"floor"`
	Mutations []mutation `yaml:"mutations"`
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
	tree := repository(t)
	var out []mutation
	for _, a := range tree.Applications(t) {
		rel := a.Root + "/" + applicationLedger
		if !tree.Exists(rel) {
			continue // an application with no proofs of its own
		}
		var l ledger
		tree.YAMLInto(t, rel, &l)
		l.Mutations = tree.ProofsIn(t, rel)
		if len(l.Mutations) < l.Floor {
			t.Fatalf("%s holds %d entries and its floor is %d. An entry was removed; "+
				"if a proof genuinely no longer applies, lower the floor in the same diff "+
				"so somebody sees it happen.", rel, len(l.Mutations), l.Floor)
		}
		for _, m := range l.Mutations {
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
	tree := repository(t)
	var l ledger
	tree.YAMLInto(t, ledgerEntries+".yml", &l)
	if len(l.Mutations) > 0 {
		t.Fatalf("tests/mutations.yml holds %d proof(s) under mutations:. Each is a file of its own under %s, named for its guard, so that two changes adding one never meet.", len(l.Mutations), ledgerEntries)
	}
	for _, rel := range tree.Files(t) {
		if filepath.Dir(rel) != ledgerEntries || filepath.Ext(rel) != ".yml" {
			continue
		}
		m := tree.Proof(t, rel)
		if m.Guard == "" || !strings.HasPrefix(m.Source, m.Guard+".") {
			t.Fatalf("%s proves the guard %q and is not named for it. A proof is found by its guard's name, so name the file <guard>.<anything>.yml.", rel, m.Guard)
		}
		l.Mutations = append(l.Mutations, m)
	}
	if len(l.Mutations) == 0 {
		t.Fatalf("%s holds no proof at all, so nothing shows any guard fails when it should", ledgerEntries)
	}
	return l
}

// An inner run is the harness's own (parsed.Inner). Everything in this file shells out to the
// test binary, so without this the runner would run itself, recursively,
// once per entry.
func skipIfInner(t *testing.T) {
	t.Helper()
	if parsed.Inner() {
		t.Skip("inner run: this is the harness, not a guard under test")
	}
}

// scratchRepo copies the tracked files into a temporary directory.
//
// Tracked files only, by way of `git ls-files`: it excludes toolshed/,
// node_modules and .git without needing a list of things to skip, and it is
// the same set a reviewer sees. Anything untracked is by definition not what a
// guard is asserting about the repository.
func scratchRepo(t *testing.T) *parsed.Tree {
	t.Helper()
	return repository(t).Copy(t)
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
func indexScratchTree(t *testing.T, scratch *parsed.Tree) {
	t.Helper()
	scratch.Index(t)
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
		if _, errs, err := repository(t).Invoke(parsed.Run{Dir: "tests/go"}, "go", "test", "-o", binPath, "-c", "./repo/"); err != nil {
			binErr = err
			binPath = ""
			t.Logf("compiling the repo test package: %v\n%s", err, errs)
		}
	})
	if binErr != nil || binPath == "" {
		t.Fatalf("could not compile the repo test package: %v", binErr)
	}
	return binPath
}

// runGuard runs exactly one test against a given tree.
func runGuard(t *testing.T, bin string, tree *parsed.Tree, name string) (passed bool, output string) {
	t.Helper()
	// Judging the tree, and started outside it, so that nothing the guard
	// names relative to where it starts is a file of the tree.
	stdout, stderr, err := tree.InvokeBinary(parsed.Run{Judged: true, Outside: true}, bin, "-test.run", "^"+name+"$", "-test.v")
	text := string(stdout) + string(stderr)
	// A -run pattern matching nothing exits 0 and is not a passing guard; it
	// is a guard that has been renamed out from under the ledger.
	if !strings.Contains(text, "RUN   "+name) {
		return false, "no test named " + name + " ran.\n" + text
	}
	return err == nil, text
}

func TestTheLedgerProvesEachGuardFailsWhenItShould(t *testing.T) {
	heavy(t, "a check on the checks: it copies the repository and runs a guard per mutation, side by side")
	skipIfInner(t)

	part := readMetaPartOrFail(t)

	since := os.Getenv(changedSinceEnv)
	change, err := changeSince(t, repository(t), since)
	if err != nil {
		t.Fatal(err)
	}

	l := readLedger(t)

	var entries []mutation
	for _, m := range append(append([]mutation{}, l.Mutations...), applicationMutations(t)...) {
		if part.proves(m.Source) && change.holds(m) {
			entries = append(entries, m)
		}
	}
	if len(entries) == 0 && since != "" {
		t.Skipf("nothing changed since %s reaches a proof; main proves them all after the merge", since)
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
			if problems := placeProblems(t, scratch, m); len(problems) > 0 {
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

			if !scratch.Apply(t, m) {
				t.Fatalf("the mutation changed nothing in the copy of %s", m.File)
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
func rulesDeclaredIn(t *testing.T, tree *parsed.Tree, dir string) map[string]bool {
	t.Helper()
	declared := map[string]bool{}
	for _, rel := range tree.Files(t) {
		// The code rules are the files of their directory; the
		// configuration rules are every policy file beneath theirs, each
		// package in a directory of its own.
		inDir, under := filepath.Dir(rel) == dir, strings.HasPrefix(rel, dir+"/")
		switch {
		case strings.HasSuffix(rel, ".yml") && inDir:
			var doc struct {
				Rules []struct {
					ID string `yaml:"id"`
				} `yaml:"rules"`
			}
			tree.YAMLInto(t, rel, &doc)
			for _, r := range doc.Rules {
				declared[r.ID] = true
			}
		case strings.HasSuffix(rel, ".rego") && under:
			if name, ok := strings.CutPrefix(regoPackage(tree.Rego(t, rel)), "rules."); ok && !isTestPackage(name) {
				declared[name] = true
			}
		}
	}
	if len(declared) == 0 {
		t.Fatalf("no rule was read from %s, so no proof's rule can be found there", dir)
	}
	return declared
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
	light(t)
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
// is written down that could stop matching how many there are. The parts
// share the ledger's proofs. Unset, as at a terminal, one run is all of them.
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
		return metaPart{}, fmt.Errorf("%s is %q, which is not a part of the meta lane: it is <index>/<total>, with at least two parts", metaPartEnv, value)
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

// proves reports whether this part proves the proof written at source. Each
// proof falls in exactly one part, by a hash of where it is written, so a
// proof added or removed moves no other.
func (p metaPart) proves(source string) bool {
	if p.whole() {
		return true
	}
	h := fnv.New32a()
	h.Write([]byte(source))
	return int(h.Sum32()%uint32(p.total)) == p.index
}

// A copy is handed the skips this run was given, and nothing when it was
// given none. Read from the pattern rather than by setting the flag: the
// flag is the whole binary's, and a guard running beside this one reads it.
func TestACopyIsSkippedWhatTheLaneSkips(t *testing.T) {
	light(t)
	if flag.Lookup("test.skip") == nil {
		t.Fatal("the test binary has no -test.skip flag to read")
	}
	if got := skipsFor(""); got != nil {
		t.Errorf("with nothing skipped, a copy was handed %v", got)
	}
	if got := skipsFor("^(TestElsewhere)$"); len(got) != 2 || got[0] != "-test.skip" || got[1] != "^(TestElsewhere)$" {
		t.Errorf("a copy was handed %v, want the lane's own skip", got)
	}
}

func TestReadMetaPartTakesOnlyAPartItCanPlace(t *testing.T) {
	light(t)
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
	light(t)
	sources := []string{"TestA.1.yml", "TestA.2.yml", "TestB.x.yml", "modules/applications/a/tests/mutations.yml#0", "", "TestC.long-name-of-a-proof.yml"}
	for _, total := range []int{2, 4, 17} {
		for _, s := range sources {
			held := 0
			for i := 0; i < total; i++ {
				if (metaPart{i, total}).proves(s) {
					held++
				}
			}
			if held != 1 {
				t.Errorf("%q falls in %d of the %d ledger parts, not one", s, held, total)
			}
		}
	}
	if !(metaPart{}).proves("TestA.1.yml") {
		t.Error("the whole of the meta lane does not prove everything")
	}
}

// unbrokenRun is a guard's answer on the unbroken tree, asked once.
type unbrokenRun struct {
	once   sync.Once
	passed bool
	output string
}

// laneSkips is the -test.skip this run was given, to hand to a run against a
// copy: the copy is judged by the guards this lane judges, and a guard the
// lane leaves to another (the coverage guard, which the Go modules lane runs
// on the profiles it measured) is left there too. Twice per application it
// was most of the removal guard's time, and past the lane's limit (#815).
func laneSkips() []string {
	f := flag.Lookup("test.skip")
	if f == nil {
		return nil
	}
	return skipsFor(f.Value.String())
}

// skipsFor is the arguments that hand a copy the skip pattern given, or
// nothing for none.
func skipsFor(pattern string) []string {
	if pattern == "" {
		return nil
	}
	return []string{"-test.skip", pattern}
}

// allVerdicts is runAll with nothing of the test in it, so that two runs
// can be made side by side: a test may only be failed from its own
// goroutine.
func allVerdicts(bin string, tree *parsed.Tree) (map[string]bool, []byte) {
	stdout, stderr, _ := tree.InvokeBinary(parsed.Run{Judged: true, Outside: true}, bin, append([]string{"-test.v"}, laneSkips()...)...)
	out := append(stdout, stderr...)

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
	light(t)
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

// placeProblems is everything wrong with a ledger entry that can be told
// without running its guard: a field it lacks, Go source it may not judge, a
// file that is not where it says or already is where it would plant one, and
// a `find` that is not exactly one place. The ledger refuses an entry for any
// of these before it runs anything, and TestEveryProofStillFindsItsPlace asks
// the same of every entry at push, so a proof left behind by a change is found
// in milliseconds rather than eight minutes into the meta lane.
func placeProblems(t *testing.T, tree *parsed.Tree, m mutation) []string {
	t.Helper()
	required := []struct{ field, value string }{
		{"guard", m.Guard}, {"file", m.File},
		{"mentions", m.Mentions}, {"why", m.Why},
	}
	if m.Creates() {
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
		declared := rulesDeclaredIn(t, tree, harness)
		if !declared[m.Rule] {
			return []string{fmt.Sprintf("this entry names the rule %s, which %s does not declare. "+
				"A rule renamed or retired leaves its proofs naming nothing: point each at the rule that refuses "+
				"what it breaks.", m.Rule, harness)}
		}
	}

	if m.Creates() {
		if tree.Exists(m.File) {
			return []string{fmt.Sprintf("this entry plants %s, and that file already exists in the "+
				"repository. Planting it would overwrite something real, and the "+
				"guard would be judged against the wrong thing.", m.File)}
		}
		return nil
	}
	n := tree.Places(t, m)
	if n < 0 {
		return []string{fmt.Sprintf("the ledger names %s, which is not there.\n\n"+
			"The file moved or was deleted. Point the entry at where it went; "+
			"do not delete the entry.", m.File)}
	}
	if n != 1 {
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
	light(t)
	tree := repository(t)
	entries := append(append([]mutation{}, readLedger(t).Mutations...), applicationMutations(t)...)
	if len(entries) < 100 {
		t.Fatalf("only %d proofs were read, which is not this ledger - the reader has stopped matching", len(entries))
	}
	for _, m := range entries {
		for _, p := range placeProblems(t, tree, m) {
			name := m.Guard
			if m.Test != "" {
				name += "/" + m.Test
			}
			t.Errorf("%s (%s): %s", name, m.File, p)
		}
	}
}
