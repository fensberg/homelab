// guard-egress refuses a workflow job whose outbound reach is not what
// scripts/approved-suppliers.yml says it is.
//
// Sixteen allowlists across nine workflows said what this estate may call out
// to, and no single place answered the question. Adding an endpoint to one
// workflow was invisible to any review not already reading that file, and a
// new job could arrive with an allowlist nobody had examined - or with none,
// which under egress-policy: block is a job that cannot run and under audit is
// a job that can reach anything.
//
// So the suppliers list owns the mapping and this refuses divergence. It walks
// EVERY job in EVERY workflow rather than the jobs it was told about: a guard
// written against a list stops covering whatever is built next, and nothing
// says so, because the new thing was never one of its subjects.
//
// Five ways to fail, and "not declared" is the important one:
//
//	a job that hardens the runner and is not named in the suppliers list
//	a job with no harden-runner step at all
//	a declared policy that is not the policy the workflow sets
//	a block job whose hosts differ from the declared set, either direction
//	an entry naming a job that no longer exists
//
// WHY THE WORKFLOW STILL CARRIES THE LIST. harden-runner installs its policy in
// its pre-step, which GitHub runs before any step of the job, and joblaw_test
// requires it to be the first step. Nothing can hand it a value read from the
// repository at runtime - not a file, not a local action, not an earlier step -
// short of a gate job every lane waits behind. The copy in the workflow is
// therefore mechanical, this file is the source of truth, and the guard is what
// makes the copy trustworthy.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"homelab/details/drafter"
	"homelab/details/suppliers"
)

// jobEgress is what a workflow says about one job's outbound reach.
type jobEgress struct {
	Workflow string // "pr-validation.yml"
	Job      string // "analyze"
	Policy   string // "block", "audit", or "" when nothing hardens the runner
	Hosts    []string
}

// Key is how a job is named in the suppliers list: the workflow file and the
// job's key, which is what does not change when somebody rewords a job name.
func (j jobEgress) Key() string { return j.Workflow + "/" + j.Job }

// declaredEgress is one entry under `egress:` in the suppliers list.
type declaredEgress struct {
	Key    string
	Policy string
	Hosts  []string
	Reason string
}

// jobsIn reports every job of a workflow, from the workflow as the drafter
// redraws it.
//
// This matched lines: a job was two spaces and a name, its policy the first
// line saying egress-policy, its hosts whatever followed allowed-endpoints
// until a line that did not look like one. It was read that way because the
// program whose subject is suppliers would take none of its own. The
// drafter takes the one there is, and a job here is a job: its key under
// `jobs`, and what its steps hand the step that hardens the runner.
func jobsIn(name string, docs []map[string]any) []jobEgress {
	var out []jobEgress
	for _, doc := range docs {
		jobs, _ := doc["jobs"].(map[string]any)
		keys := make([]string, 0, len(jobs))
		for key := range jobs {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			job, _ := jobs[key].(map[string]any)
			found := jobEgress{Workflow: name, Job: key}
			steps, _ := job["steps"].([]any)
			for _, st := range steps {
				step, _ := st.(map[string]any)
				with, _ := step["with"].(map[string]any)
				if policy, said := with[suppliers.PolicyKey].(string); said && found.Policy == "" {
					found.Policy = policy
				}
				if hosts, said := with["allowed-endpoints"].(string); said {
					found.Hosts = append(found.Hosts, strings.Fields(hosts)...)
				}
			}
			out = append(out, found)
		}
	}
	return out
}

// declarations is the `egress:` section of the suppliers list.
func declarations(body string) ([]declaredEgress, error) {
	entries, err := suppliers.EgressOf([]byte(body))
	if err != nil {
		return nil, err
	}
	var out []declaredEgress
	for _, e := range entries {
		out = append(out, declaredEgress{Key: e.Job, Policy: e.Policy, Hosts: e.Hosts, Reason: e.Reason})
	}
	return out, nil
}

// Compare reports every way the workflows and the declarations disagree.
//
// Both directions on purpose. A declaration with no job is how a list rots
// into something nobody trusts, and a job with no declaration is the case this
// guard exists for.
func Compare(found []jobEgress, declared []declaredEgress) []string {
	byKey := map[string]declaredEgress{}
	for _, d := range declared {
		byKey[d.Key] = d
	}
	seen := map[string]bool{}

	var out []string
	for _, j := range found {
		d, ok := byKey[j.Key()]
		seen[j.Key()] = true

		if j.Policy == "" {
			out = append(out, fmt.Sprintf(
				"%s hardens nothing: no step sets egress-policy, so its outbound calls are "+
					"neither restricted nor recorded", j.Key()))
			continue
		}
		if !ok {
			out = append(out, fmt.Sprintf(
				"%s is not declared in the suppliers list, so nothing says whether it may "+
					"call out at all (it sets egress-policy: %s)", j.Key(), j.Policy))
			continue
		}
		if d.Policy != j.Policy {
			out = append(out, fmt.Sprintf(
				"%s sets egress-policy: %s and the suppliers list declares %s",
				j.Key(), j.Policy, d.Policy))
			continue
		}
		if j.Policy != "block" {
			continue
		}
		for _, h := range missing(j.Hosts, d.Hosts) {
			out = append(out, fmt.Sprintf(
				"%s allows %s, which the suppliers list does not declare for it", j.Key(), h))
		}
		for _, h := range missing(d.Hosts, j.Hosts) {
			out = append(out, fmt.Sprintf(
				"%s is declared to reach %s and its workflow does not allow it", j.Key(), h))
		}
	}

	for _, d := range declared {
		if !seen[d.Key] {
			out = append(out, fmt.Sprintf(
				"the suppliers list declares %s, which is not a job in any workflow", d.Key))
		}
	}
	sort.Strings(out)
	return out
}

// missing returns the entries of a that are absent from b.
func missing(a, b []string) []string {
	in := map[string]bool{}
	for _, x := range b {
		in[x] = true
	}
	var out []string
	for _, x := range a {
		if !in[x] {
			out = append(out, x)
		}
	}
	return out
}

// ExplainEgress turns findings into the thing to do about them.
func ExplainEgress(findings []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "the workflows and scripts/approved-suppliers.yml disagree about who may call out:\n\n")
	for _, f := range findings {
		fmt.Fprintf(&b, "  %s\n", f)
	}
	fmt.Fprint(&b, `
scripts/approved-suppliers.yml owns this under 'egress:'. One entry per job,
named '<workflow file>/<job key>', carrying the policy and - for a blocking job -
the exact hosts it may reach.

A job that is not declared there is refused rather than skipped: a new job
starts with no egress until somebody says what it needs, which is the only
version of this rule that keeps covering what gets built next.

The workflow keeps a copy because harden-runner reads its policy in a pre-step,
before any step of the job can run. Change the suppliers list, then make the
workflow match it - a workflow edit is a patch under .github/patches.
`)
	return b.String()
}

// --- the verb ---------------------------------------------------------------

const workflowsDir = ".github/workflows"

func guardEgress(args []string) int {
	fs := flag.NewFlagSet("guard-egress", flag.ContinueOnError)
	// A tree to judge, for the tests that judge a copy of one.
	//
	// guard-deliveries deliberately has no such flag, because a guard that can
	// be pointed at a different list can be pointed at an empty one. The
	// difference here is that an empty tree is a refusal rather than a pass:
	// no jobs found, or no declarations found, both fail below. So pointing
	// this somewhere else cannot buy silence, and the mutation ledger needs
	// it - it proves guards against a scratch copy of the repository, which
	// has no .git for the walk upwards to find.
	root := fs.String("root", "", "the repository to judge (default: the checkout this is run from)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *root == "" {
		found, err := repositoryRoot()
		if err != nil {
			return refuseEgress(err.Error())
		}
		*root = found
	}

	suppliers, err := os.ReadFile(filepath.Join(*root, suppliersPath))
	if err != nil {
		return refuseEgress(fmt.Sprintf("cannot read %s: %v", suppliersPath, err))
	}
	declared, err := declarations(string(suppliers))
	if err != nil {
		return refuseEgress(fmt.Sprintf("cannot read %s: %v", suppliersPath, err))
	}
	if len(declared) == 0 {
		return refuseEgress(fmt.Sprintf(
			"%s declares no egress at all. Either the 'egress:' section was removed - which "+
				"would turn this guard into a no-op while looking like a passing check - or "+
				"this is not the suppliers list.", suppliersPath))
	}

	// Every workflow, redrawn in one asking. The directory also holds the
	// actions the workflows share, each in a directory of its own; a
	// workflow is a file at the top of it.
	drawn, err := drafter.Draw(*root, nil, workflowsDir)
	if err != nil {
		return refuseEgress(fmt.Sprintf("cannot read %s: %v", workflowsDir, err))
	}
	var found []jobEgress
	workflows := 0
	names := make([]string, 0, len(drawn))
	for rel := range drawn {
		names = append(names, rel)
	}
	sort.Strings(names)
	for _, rel := range names {
		name := strings.TrimPrefix(rel, workflowsDir+"/")
		if strings.Contains(name, "/") || !strings.HasSuffix(name, ".yml") {
			continue
		}
		workflows++
		var docs []map[string]any
		if err := json.Unmarshal(drawn[rel], &docs); err != nil {
			return refuseEgress(fmt.Sprintf("cannot read %s: %v", name, err))
		}
		found = append(found, jobsIn(name, docs)...)
	}

	// A walk that finds nothing passes every comparison below, which is the
	// one outcome that must never look like a clean run.
	if len(found) == 0 {
		return refuseEgress(fmt.Sprintf(
			"no jobs were found under %s, so this checked nothing", workflowsDir))
	}

	findings := Compare(found, declared)
	if len(findings) > 0 {
		fmt.Fprint(os.Stderr, "security guard-egress: "+ExplainEgress(findings))
		return 1
	}

	// Counted as they are read, not as directory entries. .github/workflows can
	// hold a subdirectory of composite action metadata, and counting entries
	// reported that as a workflow - a summary line claiming more was checked
	// than was.
	fmt.Printf("every one of the %d jobs in %d workflows reaches only what the suppliers list declares\n",
		len(found), workflows)
	return 0
}

func refuseEgress(msg string) int {
	fmt.Fprintln(os.Stderr, "security guard-egress: "+msg)
	return 1
}
