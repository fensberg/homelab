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

// Everything a local hook enforces is enforced again where nobody can skip it.
//
// A git hook is advice. `--no-verify` skips every one of them, a fresh clone
// that never ran the setup script has none installed, and a bot or an outside
// pull request never had any. So a guard that exists only as a hook is not a
// guard - it is a courtesy to whoever has it switched on.
//
// THE SHAPE TO COPY IS THE ONE THAT ALREADY WORKS. Formatting is enforced by
// `pre-commit` locally, and the Format lane runs `pre-commit run --all-files`
// against **the same .pre-commit-config.yaml**. Not the same tools - the same
// file. That is what makes drift impossible: there is no second place to say
// "markdownlint 1 here, markdownlint 2 there", because there is no second
// place at all.
//
// Where CI reimplements what a task does rather than running the task, that
// property is lost. It is not hypothetical: the Test lane kept its own list of
// Go modules to test, the taskfile kept another, and the two disagreed for
// months - two modules were on neither, so their tests ran nowhere.
//
// So this asks one mechanical question of every task a local hook invokes:
// does CI run it? A task that is a pre-commit-stage hook is covered by the
// Format lane running the whole config; anything else has to be named in a
// workflow. What is not covered is a declared debt in
// tests/coverage-blocklist.yml, which is visible and has a ceiling, rather
// than a gap nobody can see.

var taskInvocation = regexp.MustCompile(`\btask\s+([a-z][a-z0-9:-]*)`)

// localTasks is every task target a git hook or a pre-commit hook invokes,
// with whether it runs at the pre-commit stage.
func localTasks(t *testing.T) map[string]bool {
	t.Helper()
	root := repoRoot(t)
	atPreCommitStage := map[string]bool{}

	// The hook shims in githooks/ invoke tasks directly. None of these is at
	// the pre-commit stage in the pre-commit sense - they are the shims that
	// run before it.
	shims, err := os.ReadDir(filepath.Join(root, "githooks"))
	if err != nil {
		t.Fatalf("reading githooks/: %v", err)
	}
	for _, e := range shims {
		if e.IsDir() {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(root, "githooks", e.Name()))
		if readErr != nil {
			t.Fatalf("reading githooks/%s: %v", e.Name(), readErr)
		}
		for _, m := range taskInvocation.FindAllStringSubmatch(string(body), -1) {
			atPreCommitStage[m[1]] = false
		}
	}

	// The pre-commit config, where the stage decides whether the Format lane
	// already covers it.
	var config struct {
		DefaultStages []string `yaml:"default_stages"`
		Repos         []struct {
			Hooks []struct {
				Entry  string   `yaml:"entry"`
				Stages []string `yaml:"stages"`
			} `yaml:"hooks"`
		} `yaml:"repos"`
	}
	if err := yaml.Unmarshal([]byte(readRepoFile(t, ".pre-commit-config.yaml")), &config); err != nil {
		t.Fatalf("parsing .pre-commit-config.yaml: %v", err)
	}
	defaultIsPreCommit := len(config.DefaultStages) == 1 && config.DefaultStages[0] == "pre-commit"
	for _, repo := range config.Repos {
		for _, hook := range repo.Hooks {
			m := taskInvocation.FindStringSubmatch(hook.Entry)
			if m == nil {
				continue
			}
			preCommit := defaultIsPreCommit && len(hook.Stages) == 0
			for _, s := range hook.Stages {
				if s == "pre-commit" {
					preCommit = true
				}
			}
			// A task reached at more than one stage counts as covered only if
			// every route to it is covered.
			if was, seen := atPreCommitStage[m[1]]; !seen || was {
				atPreCommitStage[m[1]] = preCommit
			}
		}
	}
	if len(atPreCommitStage) == 0 {
		t.Fatal("no local hook invokes a task, which is not this repository")
	}
	return atPreCommitStage
}

// runsTask reports whether a workflow actually invokes the task, rather than
// mentioning it.
//
// Two mistakes are easy here and this file made both on its first run. A plain
// substring match counted `task test:e2e` as `task test`, so a lane that runs
// neither looked like it ran one; and it counted the mention inside a comment,
// which is the same "reading is not running" error the coverage regime exists
// to refuse. So the name is bounded and comment lines are skipped.
func runsTask(workflows, task string) bool {
	bounded := regexp.MustCompile(`\btask\s+` + regexp.QuoteMeta(task) + `(?:[^a-z0-9:_-]|$)`)
	for _, line := range strings.Split(workflows, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if bounded.MatchString(line) {
			return true
		}
	}
	return false
}

func workflowBodies(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, ".github", "workflows"))
	if err != nil {
		t.Fatalf("reading .github/workflows: %v", err)
	}
	var all strings.Builder
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(root, ".github", "workflows", e.Name()))
		if readErr != nil {
			t.Fatalf("reading %s: %v", e.Name(), readErr)
		}
		all.Write(body)
		all.WriteString("\n")
	}
	if all.Len() == 0 {
		t.Fatal("found no workflows, so every local guard would look uncovered")
	}
	return all.String()
}

func TestEveryLocalGuardIsAlsoEnforcedInCI(t *testing.T) {
	workflows := workflowBodies(t)
	// The one invocation that covers a whole class: run against the same
	// config file the hook uses, so the tools and their versions cannot differ.
	formatLaneRunsTheConfig := strings.Contains(workflows, "pre-commit run --all-files")

	blocked := blocklistEntries(t)

	var unenforced []string
	for task, preCommitStage := range localTasks(t) {
		unit := "ci:task " + task
		covered := runsTask(workflows, task) ||
			(preCommitStage && formatLaneRunsTheConfig)
		switch {
		case covered && blocked[unit]:
			t.Errorf(`%q is on the block list and CI does run it now. Remove the entry and lower the ceiling.`, unit)
		case !covered && !blocked[unit]:
			unenforced = append(unenforced, unit)
		}
	}
	sort.Strings(unenforced)

	if len(unenforced) > 0 {
		t.Errorf(`%d local guard(s) are enforced nowhere but a git hook:

  %s

`+"`--no-verify`"+` skips every hook, a fresh clone has none installed, and an
outside pull request never had any - so each of these is a courtesy rather
than a guard.

Have a workflow run the same task. Not a workflow that does the same thing:
the same task, so there is one definition and nothing to drift. The Test lane
kept its own list of Go modules while the taskfile kept another, and two
modules ended up on neither.

If it genuinely cannot run in CI yet, add it to tests/coverage-blocklist.yml
and raise the ceiling, which makes the gap visible instead of invisible.`,
			len(unenforced), strings.Join(unenforced, "\n  "))
	}
}
