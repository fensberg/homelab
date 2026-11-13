package repo

import (
	"fmt"
	"go/ast"
	"go/token"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

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

// reachOf reads a change: the paths it touched, the guards each declares,
// and whether it touched what every check runs in.
func reachOf(changed []string, guardsIn func(rel string) []string, environment func(rel string) bool) reach {
	r := reach{files: map[string]bool{}, guards: map[string]bool{}, rules: map[string]bool{}, harnesses: map[string]bool{}}
	for _, rel := range changed {
		r.files[rel] = true
		if proofMachinery(rel) || environment(rel) {
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
	r := reachOf(strings.Fields(string(out)), func(rel string) []string { return guardsDeclaredIn(t, tree, rel) }, checkEnvironment(t))
	job := findLedgerJob(t)
	before, there, err := tree.YAMLAt(since, job.workflow)
	if err != nil {
		return reach{}, fmt.Errorf("reading how %s ran the ledger at %s: %w", job.workflow, since, err)
	}
	after, _, err := tree.YAMLAt("HEAD", job.workflow)
	if err != nil {
		return reach{}, fmt.Errorf("reading how %s runs the ledger: %w", job.workflow, err)
	}
	// Not there before: the ledger's job is new, and so is everything about
	// how it runs.
	if !there || !sameLedgerJob(before, after, job.name) {
		r.whole = true
	}
	return r, nil
}

// ledgerJob is the job that runs the ledger, in the workflow it is in.
type ledgerJob struct {
	workflow, name string
	// actions are this repository's actions it runs, each followed through
	// the actions it runs in turn.
	actions []string
}

// ledgerTest is the guard the ledger is, by its own name, so a rename
// cannot leave this looking for a test nobody runs.
func ledgerTest() string {
	name := runtime.FuncForPC(reflect.ValueOf(TestTheLedgerProvesEachGuardFailsWhenItShould).Pointer()).Name()
	return name[strings.LastIndex(name, ".")+1:]
}

// laneRunsVariable is a lane that runs the tests a workflow variable names.
var laneRunsVariable = regexp.MustCompile(`^go test \./repo/ -run "\$([A-Z_]+)"`)

// findLedgerJob is the one job, in any workflow, that runs the ledger: found
// by what it runs, not by its name or its file. Its actions are followed
// from its steps through every action of this repository they run, so an
// action added to its steps, or to an action it runs, is part of what the
// ledger runs in without anybody writing it down.
func findLedgerJob(t *testing.T) ledgerJob {
	t.Helper()
	tree := repository(t)
	test := ledgerTest()
	var found []ledgerJob
	for _, rel := range trackedFiles(t) {
		if !runnable(rel) || strings.HasSuffix(rel, "/action.yml") || strings.HasSuffix(rel, "/action.yaml") {
			continue
		}
		var wf struct {
			Env  map[string]string `yaml:"env"`
			Jobs map[string]struct {
				Steps []map[string]any `yaml:"steps"`
			} `yaml:"jobs"`
		}
		tree.YAMLInto(t, rel, &wf)
		for name, job := range wf.Jobs {
			runs := false
			for _, step := range job.Steps {
				run, _ := step["run"].(string)
				if with, ok := step["with"].(map[string]any); ok && run == "" {
					run, _ = with["run"].(string)
				}
				m := laneRunsVariable.FindStringSubmatch(strings.TrimSpace(run))
				if m == nil {
					continue
				}
				selects, err := regexp.Compile(wf.Env[m[1]])
				if err == nil && wf.Env[m[1]] != "" && selects.MatchString(test) {
					runs = true
				}
			}
			if runs {
				steps := make([]any, 0, len(job.Steps))
				for _, step := range job.Steps {
					steps = append(steps, step)
				}
				found = append(found, ledgerJob{workflow: rel, name: name, actions: actionsRunBy(t, tree, scalarsAt(steps, "uses"))})
			}
		}
	}
	if len(found) != 1 {
		t.Fatalf("found %d jobs that run %s, and what the ledger runs in is read from the one that does: %v", len(found), test, found)
	}
	return found[0]
}

// actionsRunBy is the directories of this repository's actions that these
// references run, and the actions those run in turn.
func actionsRunBy(t *testing.T, tree *parsed.Tree, uses []string) []string {
	t.Helper()
	seen := map[string]bool{}
	for len(uses) > 0 {
		ref := uses[0]
		uses = uses[1:]
		dir, ours := strings.CutPrefix(ref, "$/")
		if !ours || seen[dir] {
			continue
		}
		seen[dir] = true
		meta := dir + "/action.yml"
		if !tree.Exists(meta) {
			meta = dir + "/action.yaml"
		}
		uses = append(uses, scalarsAt(tree.YAMLDocuments(t, meta), "uses")...)
	}
	dirs := make([]string, 0, len(seen))
	for dir := range seen {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs
}

// sameLedgerJob reports whether two versions of a workflow run the ledger
// the same way: the job itself, and what the workflow gives every job - its
// variables and its defaults. Another job changing is not the ledger's.
func sameLedgerJob(before, after any, job string) bool {
	given := func(doc any) map[string]any {
		wf, _ := doc.(map[string]any)
		jobs, _ := wf["jobs"].(map[string]any)
		return map[string]any{"job": jobs[job], "env": wf["env"], "defaults": wf["defaults"]}
	}
	return reflect.DeepEqual(given(before), given(after))
}

// checkEnvironment is what every proof's guard runs in: the actions the
// ledger's job runs, found from its steps, and the image the offline action
// runs it in - the build's context, what the build reads from outside it,
// and its pins - with the pools' manifests, which pin that image by digest.
// A change to any of it can alter any proof's answer, and the proofs it
// reaches are not the ones whose files it touched: #879 moved the ledger into
// an image three weeks behind its Dockerfile, its pull request proved a
// handful, and main found the rest failing. The job's own definition is
// compared in changeSince.
func checkEnvironment(t *testing.T) func(rel string) bool {
	t.Helper()
	order := pullRequestRunnerOrder(t)
	within := append([]string{order.Context}, findLedgerJob(t).actions...)
	within = append(within, order.Reads...)
	exact := map[string]bool{}
	if order.Pins != "" {
		exact[order.Pins] = true
	}
	for _, release := range runnerSets(t) {
		path, _ := fluxObject(t, kindHelmRelease, release)
		exact[path] = true
	}
	return func(rel string) bool {
		if exact[rel] {
			return true
		}
		for _, dir := range within {
			if rel == dir || strings.HasPrefix(rel, dir+"/") {
				return true
			}
		}
		return false
	}
}

func TestAChangeReachesTheProofsItCouldAlter(t *testing.T) {
	light(t)
	declares := func(rel string) []string {
		if rel == "tests/go/repo/x_test.go" {
			return []string{"TestX"}
		}
		return nil
	}
	environment := func(rel string) bool { return rel == "images/checks/Dockerfile" }
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
		{"what every check runs in", []string{"images/checks/Dockerfile"}, proof, true},
		{"nothing at all", nil, proof, false},
	} {
		if got := reachOf(c.changed, declares, environment).holds(c.proof); got != c.want {
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

// What every check runs in is found where it is declared: the action the
// offline jobs run, the runner image's build and its pins, and the manifests
// that pin it. Each reaches the whole ledger, and nothing beside them does.
func TestWhatEveryCheckRunsInReachesTheWholeLedger(t *testing.T) {
	light(t)
	in := checkEnvironment(t)
	job := findLedgerJob(t)
	if len(job.actions) == 0 {
		t.Fatalf("%s runs no action of this repository's, which is not how the ledger runs: the reading of its steps has stopped matching", job.name)
	}
	order := pullRequestRunnerOrder(t)
	path, _ := fluxObject(t, kindHelmRelease, runnerSets(t)[0])
	reached := []string{order.Context + "/Dockerfile", path}
	for _, dir := range job.actions {
		reached = append(reached, dir+"/action.yml")
	}
	for _, rel := range reached {
		if !in(rel) {
			t.Errorf("%s is part of what every check runs in, and a change to it would prove only the proofs that name it", rel)
		}
	}
	for _, rel := range []string{"docs/epochs/README.md", job.actions[0] + "-elsewhere/action.yml"} {
		if in(rel) {
			t.Errorf("%s is not what any check runs in, and a change to it would run the whole ledger", rel)
		}
	}
}

// An action the ledger's job runs is followed into the actions it runs, and
// a reference to anything that is not this repository's is not followed: a
// pinned action is held by the file that pins it, which is one of these.
func TestAnActionIsFollowedIntoTheActionsItRuns(t *testing.T) {
	light(t)
	tree := parsed.Empty(t)
	tree.Write(t, "a/action.yml", "runs:\n  using: composite\n  steps:\n    - uses: $/b\n    - uses: owner/repo@0123456789012345678901234567890123456789ab\n")
	tree.Write(t, "b/action.yaml", "runs:\n  using: composite\n  steps:\n    - uses: $/a\n")
	tree.Write(t, "c/action.yml", "runs:\n  using: composite\n  steps: []\n")
	got := actionsRunBy(t, tree, []string{"$/a", "actions/checkout@0123456789012345678901234567890123456789ab"})
	if want := []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("followed %v, want %v", got, want)
	}
}

// The ledger's job is the same when nothing it is given changed: its own
// block, and the workflow's variables and defaults. Another job is not it.
func TestTheLedgersJobIsComparedByWhatItIsGiven(t *testing.T) {
	light(t)
	base := "env:\n  A: x\ndefaults:\n  run:\n    shell: bash\njobs:\n  ledger:\n    runs-on: r\n  other:\n    runs-on: r\n"
	for _, c := range []struct {
		name  string
		after string
		same  bool
	}{
		{"nothing", base, true},
		{"another job", strings.Replace(base, "  other:\n    runs-on: r", "  other:\n    runs-on: s", 1), true},
		{"the job itself", strings.Replace(base, "  ledger:\n    runs-on: r", "  ledger:\n    runs-on: s", 1), false},
		{"a variable every job is given", strings.Replace(base, "A: x", "A: y", 1), false},
		{"the defaults", strings.Replace(base, "shell: bash", "shell: sh", 1), false},
	} {
		var before, after any
		if err := yaml.Unmarshal([]byte(base), &before); err != nil {
			t.Fatal(err)
		}
		if err := yaml.Unmarshal([]byte(c.after), &after); err != nil {
			t.Fatal(err)
		}
		if same := sameLedgerJob(before, after, "ledger"); same != c.same {
			t.Errorf("%s: same %v, want %v", c.name, same, c.same)
		}
	}
}
