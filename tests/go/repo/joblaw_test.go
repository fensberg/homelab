package repo

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Two rules about how a job starts. Both were written after finding that the
// `changes` job in deploy-infrastructure.yml began at checkout, with no
// harden-runner at all - in the workflow that applies OpenTofu. Nothing
// reported it, because a missing step is not an error anywhere: the job simply
// ran, unmonitored, and passed.
//
// A job is where a token, a runner and the network meet, so how it begins is
// the part worth making impossible to get wrong.

// harden-runner installs an eBPF agent on the runner VM and monitors egress
// from that point on. Anything before it is unwatched, so "first" is the whole
// property - a job that hardens on step four has already run three steps in
// the clear. It cannot be moved into a composite action for the same reason:
// a local action is read out of the workspace, so it cannot run before
// checkout, and hardening after checkout is the wrong order.
func TestEveryJobHardensTheRunnerFirst(t *testing.T) {
	forEachJob(t, func(t *testing.T, file, job string, steps []map[string]any) {
		if len(steps) == 0 {
			t.Errorf("%s: job %q has no steps", file, job)
			return
		}
		if uses, _ := steps[0]["uses"].(string); !strings.Contains(uses, "step-security/harden-runner@") {
			t.Errorf(`%s: job %q does not begin with harden-runner (its first step is %q).

Every step before harden-runner runs unmonitored, so this must be the first
step, not merely present. It cannot live in the setup action: a local action is
read out of the workspace and so cannot run before checkout.`, file, job, firstIdent(steps[0]))
		}
	})
}

// Everything a job needs before it can do its work - the workspace and the
// pinned versions - comes from one action, so there is one place to add the
// next thing and no way to end up with a job that skipped it. It also puts
// persist-credentials: false out of reach: leaving the token on disk lets any
// later step push with the job's credentials, and as an option per call site
// it is a thing to forget.
func TestNoWorkflowChecksOutForItself(t *testing.T) {
	dir := filepath.Join(repoRoot(t), ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		if strings.Contains(readFile(t, filepath.Join(dir, e.Name())), "actions/checkout@") {
			t.Errorf(`.github/workflows/%s calls actions/checkout directly.

Use `+"`uses: ./.github/actions/setup`"+` instead. It checks out and exports the
pinned versions, pins checkout once rather than once per job, and always passes
persist-credentials: false. Pass `+"`with: fetch-depth: 0`"+` if the job diffs
against a base.`, e.Name())
		}
	}
}

// workflowDoc is the subset of a workflow this file has an opinion about.
type workflowDoc struct {
	Jobs map[string]struct {
		Steps []map[string]any `yaml:"steps"`
	} `yaml:"jobs"`
}

func forEachJob(t *testing.T, fn func(t *testing.T, file, job string, steps []map[string]any)) {
	t.Helper()
	dir := filepath.Join(repoRoot(t), ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var seen int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		var doc workflowDoc
		if err := yaml.Unmarshal([]byte(readFile(t, filepath.Join(dir, e.Name()))), &doc); err != nil {
			t.Errorf("parsing .github/workflows/%s: %v", e.Name(), err)
			continue
		}
		names := make([]string, 0, len(doc.Jobs))
		for n := range doc.Jobs {
			names = append(names, n)
		}
		sort.Strings(names) // deterministic output
		for _, n := range names {
			seen++
			fn(t, ".github/workflows/"+e.Name(), n, doc.Jobs[n].Steps)
		}
	}
	if seen == 0 {
		t.Fatal("found no jobs in any workflow, so this test proves nothing")
	}
}

// firstIdent names a step for an error message: its `uses`, else its `name`.
func firstIdent(step map[string]any) string {
	if u, ok := step["uses"].(string); ok {
		return u
	}
	if n, ok := step["name"].(string); ok {
		return n
	}
	return "(unnamed step)"
}
