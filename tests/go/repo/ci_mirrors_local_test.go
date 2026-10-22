package repo

import (
	"homelab/tests/parsed"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
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
	tree := repository(t)
	atPreCommitStage := map[string]bool{}

	// The hook shims in githooks/ invoke tasks directly. None of these is at
	// the pre-commit stage in the pre-commit sense - they are the shims that
	// run before it.
	for _, rel := range tree.Files(t) {
		if filepath.Dir(rel) != "githooks" {
			continue
		}
		for _, c := range parsed.Commands(tree.Shell(t, rel)) {
			if task, ok := taskRun(c); ok {
				atPreCommitStage[task] = false
			}
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
	tree.YAMLInto(t, ".pre-commit-config.yaml", &config)
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

// taskRun is the task a command runs, when it runs one: a workflow that
// actually invokes it, rather than mentions it.
//
// Two mistakes are easy here and this file made both on its first run. A plain
// substring match counted `task test:e2e` as `task test`, so a lane that runs
// neither looked like it ran one; and it counted the mention inside a comment,
// which is the same "reading is not running" error the coverage regime exists
// to refuse. So the task is the command's own word, and a comment is no command.
func taskRun(c parsed.Command) (string, bool) {
	if len(c.Words) < 2 || c.Words[0] != "task" {
		return "", false
	}
	for _, w := range c.Words[1:] {
		if !strings.HasPrefix(w, "-") {
			return w, true
		}
	}
	return "", false
}

// workflowCommands is every command every workflow's steps run.
func workflowCommands(t *testing.T) []parsed.Command {
	t.Helper()
	tree := repository(t)
	var all []parsed.Command
	for _, rel := range tree.Files(t) {
		if filepath.Dir(rel) != ".github/workflows" || !strings.HasSuffix(rel, ".yml") {
			continue
		}
		var wf struct {
			Jobs map[string]struct {
				Steps []struct {
					Run string `yaml:"run"`
				} `yaml:"steps"`
			} `yaml:"jobs"`
		}
		tree.YAMLInto(t, rel, &wf)
		for _, job := range wf.Jobs {
			for _, step := range job.Steps {
				commands, err := parsed.WorkflowCommands(rel, step.Run)
				if err != nil {
					t.Fatalf("a step of %s is not shell that can be read: %v", rel, err)
				}
				all = append(all, commands...)
			}
		}
	}
	if len(all) == 0 {
		t.Fatal("found no workflow steps, so every local guard would look uncovered")
	}
	return all
}

func TestEveryLocalGuardIsAlsoEnforcedInCI(t *testing.T) {
	workflows := workflowCommands(t)
	ran := map[string]bool{}
	formatLaneRunsTheConfig := false
	for _, c := range workflows {
		if task, ok := taskRun(c); ok {
			ran[task] = true
		}
		// The one invocation that covers a whole class: run against the same
		// config file the hook uses, so the tools and their versions cannot differ.
		formatLaneRunsTheConfig = formatLaneRunsTheConfig ||
			len(c.Words) >= 3 && slices.Equal(c.Words[:3], []string{"pre-commit", "run", "--all-files"})
	}

	blocked := blocklistEntries(t)

	var unenforced []string
	for task, preCommitStage := range localTasks(t) {
		unit := "ci:task " + task
		covered := ran[task] ||
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
