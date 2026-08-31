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

// Checkout has to be written out in each job, because a local action cannot
// wrap it: `uses: ./...` is read out of the workspace, so nothing local can run
// until checkout has produced one. An earlier version of this file tried
// exactly that - a setup action that did the checkout - and every job failed
// with "Can't find 'action.yml' ... Did you forget to run actions/checkout
// before running your local action?". The constraint is real and there is no
// way around it for a local action.
//
// So the duplication stays and the property is asserted instead.
// persist-credentials: false is the part worth enforcing: leaving the token on
// disk lets any later step, or anything a later step runs, push with the job's
// credentials. No job here needs it, and omitting it is silent - the checkout
// succeeds either way.
func TestEveryCheckoutRefusesToPersistCredentials(t *testing.T) {
	forEachJob(t, func(t *testing.T, file, job string, steps []map[string]any) {
		for _, step := range steps {
			uses, _ := step["uses"].(string)
			if !strings.Contains(uses, "actions/checkout@") {
				continue
			}
			with, _ := step["with"].(map[string]any)
			if v, ok := with["persist-credentials"]; !ok || v != false {
				t.Errorf(`%s: job %q checks out without persist-credentials: false.

The job's token stays on disk for every later step otherwise, and anything one
of those steps runs can push with it. Nothing here needs it, and leaving it out
is silent - the checkout succeeds either way.`, file, job)
			}
		}
	})
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
