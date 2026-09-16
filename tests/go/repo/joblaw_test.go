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
// the clear.
//
// FIRST MAY MEAN THE FIRST STEP OF THE JOB'S FIRST ACTION. A job may begin with
// a composite action from this repository, provided that action's own first
// step is harden-runner. That is how harden-runner and checkout come to be
// written once rather than in every job.
//
// This used to be refused outright, and for a reason that was true at the time:
// the only same-repository reference was `./`, which is read out of the
// workspace, so nothing local could run before checkout. An action that tried -
// .github/actions/secure-checkout - failed in every job with "Can't find
// 'action.yml' ... Did you forget to run actions/checkout". GitHub's `$/`
// reference resolves at the running commit with no checkout, which removes that
// constraint and nothing else.
//
// WHAT THIS DOES NOT PROVE. That harden-runner ENFORCES when nested. It installs
// its policy in a pre-step, and GitHub documents pre-steps as unsupported for
// local actions. A job can satisfy this rule and be unwatched. The answer to
// that is not in the shape of the YAML, so it is not asserted here: it is
// .github/workflows/egress-proof.yml, which requires a request outside the
// allowlist to be refused.
func TestEveryJobHardensTheRunnerFirst(t *testing.T) {
	forEachJob(t, func(t *testing.T, file, job string, steps []map[string]any) {
		if len(steps) == 0 {
			t.Errorf("%s: job %q has no steps", file, job)
			return
		}
		first, _ := steps[0]["uses"].(string)
		if strings.Contains(first, "step-security/harden-runner@") {
			return
		}

		if action, ok := sameRepoAction(first); ok {
			inner, err := localActionSteps(t, action)
			if err != nil {
				t.Errorf(`%s: job %q begins with %s, and that action cannot be read: %v

A job whose first step is an action nobody can inspect is a job nobody can say
is hardened. If it is meant to exist, it must be in this repository at the path
the reference names.`, file, job, first, err)
				return
			}
			if len(inner) > 0 {
				if u, _ := inner[0]["uses"].(string); strings.Contains(u, "step-security/harden-runner@") {
					return
				}
			}
			ident := "(no steps)"
			if len(inner) > 0 {
				ident = firstIdent(inner[0])
			}
			t.Errorf(`%s: job %q begins with %s, whose own first step is %q rather than
harden-runner.

A job may start with an action from this repository only when that action
hardens the runner before doing anything else. Otherwise every step inside it
runs unmonitored while the job looks like it starts correctly.`, file, job, first, ident)
			return
		}

		t.Errorf(`%s: job %q does not begin with harden-runner (its first step is %q).

Every step before harden-runner runs unmonitored, so this must be the first
step, not merely present - either directly, or as the first step of a composite
action from this repository referenced with $/.`, file, job, firstIdent(steps[0]))
	})
}

// persist-credentials: false is the part of checkout worth enforcing: leaving
// the token on disk lets any later step, or anything a later step runs, push
// with the job's credentials. No job here needs it, and omitting it is silent -
// the checkout succeeds either way.
//
// A checkout inside a composite action from this repository counts, because a
// job calling that action has checked out just the same. Reading only the
// job's own steps would have made moving checkout into an action a way to stop
// this rule applying without anybody deciding it should.
func TestEveryCheckoutRefusesToPersistCredentials(t *testing.T) {
	forEachJob(t, func(t *testing.T, file, job string, steps []map[string]any) {
		check := func(where string, steps []map[string]any) {
			for _, step := range steps {
				uses, _ := step["uses"].(string)
				if !strings.Contains(uses, "actions/checkout@") {
					continue
				}
				with, _ := step["with"].(map[string]any)
				if v, ok := with["persist-credentials"]; !ok || v != false {
					t.Errorf(`%s: job %q checks out without persist-credentials: false%s.

The job's token stays on disk for every later step otherwise, and anything one
of those steps runs can push with it. Nothing here needs it, and leaving it out
is silent - the checkout succeeds either way.`, file, job, where)
				}
			}
		}

		check("", steps)
		for _, step := range steps {
			uses, _ := step["uses"].(string)
			action, ok := sameRepoAction(uses)
			if !ok {
				continue
			}
			inner, err := localActionSteps(t, action)
			if err != nil {
				t.Errorf("%s: job %q uses %s, which cannot be read: %v", file, job, uses, err)
				continue
			}
			check(" (inside "+uses+")", inner)
		}
	})
}

// sameRepoAction reports the repository path a same-repository action
// reference points at: `$/path` and `./path` both name a directory here.
func sameRepoAction(uses string) (string, bool) {
	for _, prefix := range []string{"$/", "./"} {
		if strings.HasPrefix(uses, prefix) {
			return strings.TrimSuffix(strings.TrimPrefix(uses, prefix), "/"), true
		}
	}
	return "", false
}

// localActionSteps reads the steps of a composite action in this repository.
func localActionSteps(t *testing.T, dir string) ([]map[string]any, error) {
	t.Helper()
	root := repoRoot(t)
	for _, name := range []string{"action.yml", "action.yaml"} {
		body, err := os.ReadFile(filepath.Join(root, dir, name))
		if err != nil {
			continue
		}
		var doc struct {
			Runs struct {
				Using string           `yaml:"using"`
				Steps []map[string]any `yaml:"steps"`
			} `yaml:"runs"`
		}
		if err := yaml.Unmarshal(body, &doc); err != nil {
			return nil, err
		}
		if doc.Runs.Using != "composite" {
			return nil, os.ErrInvalid
		}
		return doc.Runs.Steps, nil
	}
	return nil, os.ErrNotExist
}

// A job with no timeout-minutes gets GitHub's default of 360 - six hours. For
// a lane that normally finishes in two minutes that is not a safety margin, it
// is six hours of a stuck loop calling whatever the job can reach.
//
// That matters here beyond wasted runner time. This estate's workflows talk to
// Cloudflare, which holds a real credit card, and object storage is billed per
// operation. A wedged job retrying against R2 is not a hung build, it is an
// invoice. Twelve of nineteen jobs were unbounded when this was written.
//
// The rule is every job rather than only the ones that touch a vendor today,
// because which jobs those are changes without anyone re-reading this, and a
// bound on a cheap job costs nothing.
func TestEveryJobIsBounded(t *testing.T) {
	forEachJob(t, func(t *testing.T, file, job string, steps []map[string]any) {
		if jobTimeouts[file+"::"+job] == 0 {
			t.Errorf(`%s: job %q declares no timeout-minutes.

It therefore inherits GitHub's default of 360 - six hours in which a stuck job
keeps calling whatever it can reach, including metered vendors. Give it a bound
comfortably above its normal runtime.`, file, job)
		}
	})
}

// workflowDoc is the subset of a workflow this file has an opinion about.
type workflowDoc struct {
	Jobs map[string]struct {
		Steps   []map[string]any `yaml:"steps"`
		Timeout int              `yaml:"timeout-minutes"`
	} `yaml:"jobs"`
}

// jobTimeouts is filled by forEachJob, keyed "<file>::<job>". Kept beside the
// walk rather than widening its callback, which every other law here ignores.
var jobTimeouts = map[string]int{}

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
			file := ".github/workflows/" + e.Name()
			jobTimeouts[file+"::"+n] = doc.Jobs[n].Timeout
			fn(t, file, n, doc.Jobs[n].Steps)
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
