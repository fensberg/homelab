// guard-egress refuses a workflow job that scripts/approved-suppliers.yml does
// not name, and an entry there that names no job.
//
// Sixteen allowlists across nine workflows once said what this estate may call
// out to, and no single place answered the question. The suppliers list owns
// that under `egress:`, one entry per job. It walks EVERY job in EVERY
// workflow rather than the jobs it was told about: a guard written against a
// list stops covering whatever is built next, and nothing says so.
//
// Two ways to fail, and "not declared" is the important one:
//
//	a job the suppliers list does not name
//	an entry naming a job that no longer exists
//
// WHAT NO LONGER HOLDS. The workflows carried a copy of each list for
// harden-runner, which read it in a pre-step, and this refused any drift
// between the copy and the list. The operator cut harden-runner from every
// job on 2026-11-12 (#795), so nothing on GitHub's runners holds a job to its
// list: what a job reaches is declared and not enforced there, until epoch
// 16, decision 16 runs a pull request's code with no network. The pools are
// still held to their jobs' lists by the cluster's network.
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

// jobEgress is one job of a workflow, by the name the suppliers list knows
// it by.
type jobEgress struct {
	Workflow string // "pr-validation.yml"
	Job      string // "analyze"
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
// redraws it: its key under `jobs`.
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
			out = append(out, jobEgress{Workflow: name, Job: key})
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

// Compare reports every job the declarations do not name, and every
// declaration that names no job.
//
// Both directions on purpose. A declaration with no job is how a list rots
// into something nobody trusts, and a job with no declaration is the case this
// guard exists for.
func Compare(found []jobEgress, declared []declaredEgress) []string {
	named := map[string]bool{}
	for _, d := range declared {
		named[d.Key] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, j := range found {
		seen[j.Key()] = true
		if !named[j.Key()] {
			out = append(out, fmt.Sprintf(
				"%s is not declared in the suppliers list, so nothing says what it may reach", j.Key()))
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

Nothing on GitHub's runners holds a job to its entry since harden-runner was
cut (#795). The entry is still what the pools are held to, and what epoch 16,
decision 16 holds every job to again.
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
	fmt.Printf("every one of the %d jobs in %d workflows is declared in the suppliers list\n",
		len(found), workflows)
	return 0
}

func refuseEgress(msg string) int {
	fmt.Fprintln(os.Stderr, "security guard-egress: "+msg)
	return 1
}
