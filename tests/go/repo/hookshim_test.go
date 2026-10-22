package repo

import (
	"homelab/tests/parsed"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The supplier guard has to run before pre-commit, not as one of its hooks.
//
// pre-commit clones a hook repository and installs its environment before it
// runs any hook, and installing executes setup code - npm install, pip
// install, a Go build. A guard that is itself a pre-commit hook therefore runs
// after the code it exists to refuse has already executed on the machine
// holding the vault session. Gating execution is not gating download.
//
// git invokes githooks/pre-commit. Nothing third-party has run at that point.
// So the guard belongs there, and these assert it is there, that it comes
// first, and that git is actually pointed at it - because a shim nothing
// invokes is a file.

func TestTheHookShimRunsTheGuardBeforePreCommit(t *testing.T) {
	for _, hook := range []string{"pre-commit", "pre-push"} {
		commands := readShim(t, hook)

		guard := slices.IndexFunc(commands, runsTheDeliveryGuard)
		precommit := slices.IndexFunc(commands, func(c parsed.Command) bool {
			return len(c.Words) > 1 && c.Words[0] == "exec" && c.Words[1] == "pre-commit"
		})

		if guard < 0 {
			t.Errorf("githooks/%s does not run `security guard-deliveries`, so an unapproved "+
				"repository would be cloned and installed before anything checked it", hook)
			continue
		}
		if precommit < 0 {
			t.Errorf("githooks/%s never reaches pre-commit, so no hook runs at all", hook)
			continue
		}
		if guard > precommit {
			t.Errorf("githooks/%s runs the guard after pre-commit. By then the clone and "+
				"the install have happened, which is the whole thing this ordering "+
				"exists to prevent.", hook)
		}
	}
}

// A shim that exits zero on a failing guard is a shim that reports rather than
// refuses. `set -e` is what makes the guard's exit code stop the commit.
func TestTheHookShimFailsOnAFailingGuard(t *testing.T) {
	for _, hook := range []string{"pre-commit", "pre-push", "commit-msg"} {
		setsE := slices.ContainsFunc(readShim(t, hook), func(c parsed.Command) bool {
			return slices.Equal(c.Words, []string{"set", "-euo", "pipefail"})
		})
		if !setsE {
			t.Errorf("githooks/%s does not set -e, so a failing guard would be reported "+
				"and the commit would proceed anyway", hook)
		}
	}
}

// git will not run a shim it cannot execute, and will not say so loudly.
func TestTheHookShimsAreExecutable(t *testing.T) {
	for _, hook := range []string{"pre-commit", "pre-push", "commit-msg"} {
		info, err := repository(t).Stat("githooks/" + hook)
		if err != nil {
			t.Fatalf("githooks/%s: %v", hook, err)
		}
		if info.Mode()&0o111 == 0 {
			t.Errorf("githooks/%s is not executable, so git silently skips it and every "+
				"hook stops running", hook)
		}
	}
}

// The shim only guards anything if git is pointed at it. Left unset, git uses
// .git/hooks - which pre-commit owns and overwrites - and the guard is a file
// nobody runs.
func TestSetupPointsGitAtTheShims(t *testing.T) {
	setup := parsed.Commands(repository(t).Shell(t, "scripts/install-dependencies.sh"))
	points := slices.ContainsFunc(setup, func(c parsed.Command) bool {
		i := slices.Index(c.Words, "core.hooksPath")
		return len(c.Words) > 0 && c.Words[0] == "git" && i >= 0 && i+1 < len(c.Words) && c.Words[i+1] == "githooks"
	})
	if !points {
		t.Fatal("setup does not point git at githooks/, so the shims are files nobody " +
			"runs and pre-commit's own hook is what git invokes")
	}
	if slices.ContainsFunc(setup, func(c parsed.Command) bool { return slices.Equal(c.Words, []string{"pre-commit", "install"}) }) {
		t.Error("setup still runs `pre-commit install`, which writes .git/hooks/pre-commit " +
			"and takes precedence over nothing - but it is the mechanism the shim " +
			"replaces, and leaving both means whichever git picks decides whether the " +
			"guard runs")
	}
}

// readShim is every command a hook shim runs, in order.
func readShim(t *testing.T, hook string) []parsed.Command {
	t.Helper()
	return parsed.Commands(repository(t).Shell(t, "githooks/"+hook))
}

// runsTheDeliveryGuard reports whether a command is the security program's
// guard-deliveries verb.
func runsTheDeliveryGuard(c parsed.Command) bool {
	return len(c.Words) > 1 && filepath.Base(c.Words[0]) == "security" && c.Words[1] == "guard-deliveries"
}

// The Format lane runs the same .pre-commit-config.yaml a developer runs, and
// installs pre-commit and nothing else. So a `language: system` hook - one
// that shells out to a binary rather than to an environment pre-commit built -
// has nothing to shell out to there, and fails with "executable not found".
//
// That is a red check saying nothing about the repository, and it happened:
// delivery-guard's entry is `task delivery-guard`, the lane has no `task`, and
// the whole lane failed on a hook that could never have run in it.
//
// The rule is that every system hook is skipped in that lane and owned by a
// lane that has what it needs - tofu-fmt and go-fmt already were. Written as a
// comment it would hold until the next hook; written here, adding one fails
// this test with the two names that disagree.
//
// ONE EXCEPTION, AND IT IS EARNED NOT DECLARED. A system hook may run in the
// lane when its entry is `delivered-python -m <module>` and the lane takes delivery of
// that module's package from scripts/deliveries.lock before running pre-commit.
// The pre-commit-hooks checks are that shape (#416): installed by hash rather
// than cloned by pre-commit, so the lane has exactly what they need. There is
// no list of such hooks here - the entry, the lock and the lane's own
// take-delivery step have to agree, and a hook that stops agreeing is refused
// like any other.
func TestEverySystemHookIsSkippedInTheFormatLane(t *testing.T) {
	cfg := parsePreCommitConfig(t)

	defaults := cfg.DefaultStages
	if len(defaults) == 0 {
		defaults = []string{"pre-commit"}
	}

	runsAtCommit := func(stages []string) bool {
		if len(stages) == 0 {
			stages = defaults
		}
		for _, s := range stages {
			if s == "pre-commit" {
				return true
			}
		}
		return false
	}

	system := map[string]bool{}
	entries := map[string]string{}
	for _, repo := range cfg.Repos {
		for _, hook := range repo.Hooks {
			if hook.Language == "system" && runsAtCommit(hook.Stages) {
				system[hook.ID] = true
				entries[hook.ID] = hook.Entry
			}
		}
	}
	if len(system) == 0 {
		t.Fatal("no system hook runs at the pre-commit stage, so this test is " +
			"comparing an empty set against the skip list and proving nothing")
	}

	workflow := repository(t).YAMLDocuments(t, workflowPath("pr-validation.yml"))
	skip, ok := scalarAt(workflow, "SKIP")
	if !ok {
		t.Fatal("the Format lane no longer sets SKIP, so every system hook now runs " +
			"there - each will fail on a binary that lane does not install")
	}
	skipped := map[string]bool{}
	for _, name := range strings.Split(skip, ",") {
		if name = strings.TrimSpace(name); name != "" {
			skipped[name] = true
		}
	}

	delivered := formatLaneDeliveries(t, workflow)
	lock := repository(t).DeliveriesLock(t)
	for id := range system {
		if !skipped[id] {
			if pkg, ok := lockedPythonPackage(entries[id]); ok {
				if !delivered[pkg] {
					t.Errorf("hook %q runs `%s`, and the Format lane does not take delivery of %s "+
						"before running pre-commit.\n\n"+
						"It will run there and fail with \"No module named\". Add %s to the lane's "+
						"scripts/take-delivery.sh step, or skip the hook and name its owner.",
						id, entries[id], pkg, pkg)
				} else if _, locked := lock["pypi:"+pkg]; !locked {
					t.Errorf("hook %q runs `%s`, and scripts/deliveries.lock has no pypi: section for %s, "+
						"so the lane's delivery of it fails before any hook runs", id, entries[id], pkg)
				}
				continue
			}
			t.Errorf("hook %q is a system hook at the pre-commit stage and is not in "+
				"the Format lane's SKIP list.\n\n"+
				"It will run there and fail on a binary that lane does not install. "+
				"Add it to SKIP in .github/workflows/pr-validation.yml, and say in the "+
				"comment above which lane or test owns the check instead - a skip with "+
				"no owner is the check switched off.", id)
		}
	}
	for id := range skipped {
		if !system[id] {
			t.Errorf("the Format lane skips %q, which is not a system hook running at "+
				"the pre-commit stage.\n\n"+
				"Either it was renamed and the skip now silences nothing, or a hook "+
				"pre-commit can run perfectly well is being skipped for no reason.", id)
		}
	}
}

// formatLaneDeliveries is every name the Format lane passes to
// scripts/take-delivery.sh before it runs pre-commit.
func formatLaneDeliveries(t *testing.T, workflow []any) map[string]bool {
	t.Helper()
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if len(workflow) != 1 || as(workflow[0], &wf) != nil {
		t.Fatal("pr-validation.yml is not one workflow that can be read")
	}
	lane, ok := wf.Jobs["format"]
	if !ok {
		t.Fatal("pr-validation.yml has no format job, so which packages the Format lane installs cannot be read")
	}
	out := map[string]bool{}
	for _, step := range lane.Steps {
		script, err := parsed.ShellOf("the Format lane", step.Run)
		if err != nil {
			t.Fatalf("a step of the Format lane is not shell that can be read: %v", err)
		}
		for _, c := range parsed.Commands(script) {
			if len(c.Words) >= 3 && slices.Equal(c.Words[:3], []string{"pre-commit", "run", "--all-files"}) {
				return out
			}
			if len(c.Words) > 0 && strings.HasSuffix(c.Words[0], "scripts/take-delivery.sh") {
				for _, name := range c.Words[1:] {
					out[name] = true
				}
			}
		}
	}
	t.Fatal("the Format lane does not run pre-commit, so nothing it installs can be judged against the hooks")
	return nil
}

// lockedPythonPackage reads `delivered-python -m <module>...` and returns the PyPI name
// the module's top-level package is published under, the way the lock writes it.
func lockedPythonPackage(entry string) (string, bool) {
	m := regexp.MustCompile(`^delivered-python -m ([a-z0-9_]+)(?:\.|\s|$)`).FindStringSubmatch(strings.TrimSpace(entry))
	if m == nil {
		return "", false
	}
	return strings.ReplaceAll(m[1], "_", "-"), true
}

// core.hooksPath REPLACES .git/hooks; it does not merge with it.
//
// That single fact is what made this necessary. `.pre-commit-config.yaml`
// declares default_install_hook_types, `pre-commit install` writes a hook for
// each into .git/hooks, and this repository then points git somewhere else
// entirely. Any stage without a shim in githooks/ has a hook that exists, is
// correct, and is never invoked.
//
// commit-msg spent that way silently. Two commitlint rules this repository
// holds deliberately - no self co-authorship, and a lower-case subject - were
// enforced only by CI, so each violation cost a push and a red Analyze rather
// than a second at the terminal. The second cost a rewritten branch and a
// replaced pull request, because a published commit subject cannot be amended.
//
// Asserted as a class rather than as "commit-msg exists", because the failure
// is not that one hook was missing. It is that adding a stage to the config
// looks complete and is not, and the next stage added would go the same way.
func TestEveryInstalledHookTypeHasAShim(t *testing.T) {
	var cfg struct {
		Types []string `yaml:"default_install_hook_types"`
	}
	repository(t).YAMLInto(t, ".pre-commit-config.yaml", &cfg)
	stages := cfg.Types
	if len(stages) == 0 {
		t.Fatal("default_install_hook_types is empty, so this check is asserting nothing")
	}

	for _, stage := range stages {
		if !repository(t).Exists("githooks/" + stage) {
			t.Errorf("%q is in default_install_hook_types but githooks/%s does not exist.\n\ncore.hooksPath replaces .git/hooks rather than merging with it, so the hook pre-commit installs for this stage is never invoked. It looks installed and does nothing.", stage, stage)
		}
	}
}
