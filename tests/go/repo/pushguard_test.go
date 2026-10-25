package repo

import (
	"slices"
	"testing"
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
// So the rule is enforced by a pre-push hook, and a configuration rule keeps
// the hook wired (policy/conftest/rules/push_hooks/push_hooks.rego). What is left here is that
// the hook and signedpush agree on the one ref a push may go to.
type preCommitConfig struct {
	DefaultStages []string `yaml:"default_stages"`
	Repos         []struct {
		Repo  string `yaml:"repo"`
		Hooks []struct {
			ID       string   `yaml:"id"`
			Entry    string   `yaml:"entry"`
			Language string   `yaml:"language"`
			Stages   []string `yaml:"stages"`
			FailFast bool     `yaml:"fail_fast"`
		} `yaml:"hooks"`
	} `yaml:"repos"`
}

func parsePreCommitConfig(t *testing.T) preCommitConfig {
	t.Helper()
	var cfg preCommitConfig
	repository(t).YAMLInto(t, ".pre-commit-config.yaml", &cfg)
	if len(cfg.Repos) == 0 {
		t.Fatal("the hook configuration parsed to nothing, so every test reading it proves nothing")
	}
	return cfg
}

// The guard allows signedpush's scratch ref and refuses branch refs. If
// signedpush ever changes that namespace, the guard would start refusing the
// one push it is supposed to permit - so the two are checked against each
// other here as well as in scripts/superintendent's own tests.
func TestGuardAndSignedpushAgreeOnTheScratchRef(t *testing.T) {
	const scratch = "refs/signing/"
	if !slices.Contains(stringLiterals(t, "scripts/superintendent/enforce-push.go"), scratch) {
		t.Errorf("pushguard no longer allows %s", scratch)
	}
	if !slices.Contains(stringLiterals(t, "scripts/signedpush/main.go"), scratch) {
		t.Errorf("signedpush no longer pushes to %s, so pushguard is now "+
			"refusing the only supported way to publish", scratch)
	}
}
