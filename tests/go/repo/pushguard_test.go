package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A plain `git push` must stay refused.
//
// A GitHub App cannot hold a signing key, so commits are signed by GitHub on
// the App's behalf through the Git Data API - which is what `task push`
// does. A plain `git push` also works, and produces unsigned commits
// attributed to whatever local git config happens to say.
//
// That was documented in CLAUDE.md, in the taskfile and in signedpush's own
// header, and documentation was not enough: a session pushed twice with plain
// git before anyone noticed. The commits could not be repaired afterwards
// because `non_fast_forward` applies to feature branches here too, so there
// is no force-push available to anybody, agent or admin. The only remedy was
// a new branch and a new pull request.
//
// So the rule is enforced by a pre-push hook, and this test is what keeps the
// hook wired. Deleting the hook to make a push go through is exactly the
// shortcut this exists to catch.
type preCommitConfig struct {
	Repos []struct {
		Repo  string `yaml:"repo"`
		Hooks []struct {
			ID       string   `yaml:"id"`
			Entry    string   `yaml:"entry"`
			Stages   []string `yaml:"stages"`
			FailFast bool     `yaml:"fail_fast"`
		} `yaml:"hooks"`
	} `yaml:"repos"`
}

const pushGuardHookID = "push-guard"

func TestPlainGitPushIsRefusedByAHook(t *testing.T) {
	root := repoRoot(t)

	body, err := os.ReadFile(filepath.Join(root, ".pre-commit-config.yaml"))
	if err != nil {
		t.Fatalf("reading .pre-commit-config.yaml: %v", err)
	}

	var cfg preCommitConfig
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("parsing .pre-commit-config.yaml: %v", err)
	}

	var found, failFast bool
	var stages []string
	var order []string
	for _, repo := range cfg.Repos {
		for _, hook := range repo.Hooks {
			for _, stage := range hook.Stages {
				if stage == "pre-push" {
					order = append(order, hook.ID)
				}
			}
			if hook.ID == pushGuardHookID {
				found = true
				stages = hook.Stages
				failFast = hook.FailFast
			}
		}
	}

	if !found {
		t.Fatalf("no %q hook in .pre-commit-config.yaml.\n"+
			"A plain `git push` produces unsigned commits that nobody can rewrite "+
			"afterwards, because non_fast_forward applies to feature branches here. "+
			"Publish with `task push`; do not remove the hook that enforces it.",
			pushGuardHookID)
	}

	if len(stages) != 1 || stages[0] != "pre-push" {
		t.Errorf("%s runs at stages %v; it only guards anything at pre-push",
			pushGuardHookID, stages)
	}

	// Ordering alone does not short-circuit anything: pre-commit runs every
	// hook and reports at the end. Without fail_fast a refused push still
	// paid for the whole Go and OpenTofu corpus first, which was measured
	// rather than assumed. Both are needed, so both are checked.
	if !failFast {
		t.Errorf("%s is not fail_fast, so a refused push still runs validate "+
			"and test before printing the refusal", pushGuardHookID)
	}

	if len(order) > 0 && order[0] != pushGuardHookID {
		t.Errorf("pre-push hooks run in order %v; %s should be first so a "+
			"doomed push fails fast instead of after validate and test",
			order, pushGuardHookID)
	}
}

// The hook shells out to `task push-guard`, so that task has to exist and has
// to be callable from outside. It was briefly marked `internal: true`, which
// task refuses to run from the CLI - the hook failed with a task error rather
// than a verdict, which is a guard that is broken in the direction of noise.
func TestPushGuardTaskIsInvokable(t *testing.T) {
	root := repoRoot(t)

	body, err := os.ReadFile(filepath.Join(root, "taskfile.yml"))
	if err != nil {
		t.Fatalf("reading taskfile.yml: %v", err)
	}

	var tf struct {
		Tasks map[string]struct {
			Internal bool `yaml:"internal"`
		} `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(body, &tf); err != nil {
		t.Fatalf("parsing taskfile.yml: %v", err)
	}

	task, ok := tf.Tasks[pushGuardHookID]
	if !ok {
		t.Fatalf("taskfile.yml has no %q task, but .pre-commit-config.yaml "+
			"invokes `task %s`", pushGuardHookID, pushGuardHookID)
	}
	if task.Internal {
		t.Errorf("the %s task is marked internal, so `task %s` fails from the "+
			"CLI and the pre-push hook cannot run it", pushGuardHookID, pushGuardHookID)
	}
}

// The guard allows signedpush's scratch ref and refuses branch refs. If
// signedpush ever changes that namespace, the guard would start refusing the
// one push it is supposed to permit - so the two are checked against each
// other here as well as in scripts/pushguard's own tests.
func TestGuardAndSignedpushAgreeOnTheScratchRef(t *testing.T) {
	root := repoRoot(t)

	guard, err := os.ReadFile(filepath.Join(root, "scripts", "pushguard", "main.go"))
	if err != nil {
		t.Fatalf("reading scripts/pushguard/main.go: %v", err)
	}
	signed, err := os.ReadFile(filepath.Join(root, "scripts", "signedpush", "main.go"))
	if err != nil {
		t.Fatalf("reading scripts/signedpush/main.go: %v", err)
	}

	const scratch = `"refs/signing/"`
	if !strings.Contains(string(guard), scratch) {
		t.Errorf("pushguard no longer allows %s", scratch)
	}
	if !strings.Contains(string(signed), scratch) {
		t.Errorf("signedpush no longer pushes to %s, so pushguard is now "+
			"refusing the only supported way to publish", scratch)
	}
}
