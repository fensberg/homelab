package repo

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// How the guards that still read workflows in Go find a uses line and tell
// a file GitHub executes. The rule that once lived beside these is a
// configuration rule now (policy/conftest/action_commits.rego).

var (
	usesLine     = regexp.MustCompile(`(?m)^\s*(?:-\s+)?uses:\s*["']?([^\s"'#]+)`)
	pinnedAction = regexp.MustCompile(`^([A-Za-z0-9._-]+/[A-Za-z0-9._-]+)((?:/[^@]+)?)@([0-9a-f]{40})$`)
)

// runnable reports whether GitHub executes a file's `uses:` lines: a workflow
// at the top of .github/workflows, or the metadata of any composite action.
func runnable(rel string) bool {
	base := filepath.Base(rel)
	if base == "action.yml" || base == "action.yaml" {
		return true
	}
	dir := filepath.ToSlash(filepath.Dir(rel))
	return dir == ".github/workflows" &&
		(strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".yaml"))
}

// jobsOf is a workflow's jobs, with what these tests ask of each.
type jobOf struct {
	Permissions map[string]string `yaml:"permissions"`
	Steps       []struct {
		Name string            `yaml:"name"`
		ID   string            `yaml:"id"`
		If   string            `yaml:"if"`
		Uses string            `yaml:"uses"`
		Run  string            `yaml:"run"`
		Env  map[string]string `yaml:"env"`
		With map[string]string `yaml:"with"`
	} `yaml:"steps"`
}

func jobsOf(t *testing.T, workflow string) map[string]jobOf {
	t.Helper()
	var wf struct {
		Jobs map[string]jobOf `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(workflowText(t, workflow)), &wf); err != nil {
		t.Fatalf("parsing %s: %v", workflow, err)
	}
	if len(wf.Jobs) == 0 {
		t.Fatalf("%s has no jobs this can read", workflow)
	}
	return wf.Jobs
}
