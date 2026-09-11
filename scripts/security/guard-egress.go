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
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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

var (
	topLevelKey = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*:`)
	jobKey      = regexp.MustCompile(`^  ([a-zA-Z0-9_-]+):\s*$`)
	hostEntry   = regexp.MustCompile(`^\s+- ?([a-z0-9.*-]+:\d+)\s*$`)
	bareHost    = regexp.MustCompile(`^\s+([a-z0-9.*-]+:\d+)\s*$`)
	policyLine  = regexp.MustCompile(`^\s+egress-policy:\s*(\w+)`)
)

// workflowEgress reads one workflow and reports every job in it.
//
// Hand-parsed rather than unmarshalled, for the reason reposIn gives: this is
// the program whose whole subject is suppliers, and a YAML library would be one
// more supplier to approve. The shape it depends on - two-space job keys under
// a top-level `jobs:` - is the shape prettier and actionlint already enforce on
// every file here.
func workflowEgress(name, body string) []jobEgress {
	var out []jobEgress
	var current *jobEgress
	inJobs := false
	inList := false

	flush := func() {
		if current != nil {
			out = append(out, *current)
			current = nil
		}
	}

	for _, line := range strings.Split(body, "\n") {
		if topLevelKey.MatchString(line) {
			inJobs = strings.HasPrefix(line, "jobs:")
			inList = false
		}
		if m := jobKey.FindStringSubmatch(line); m != nil && inJobs {
			flush()
			current = &jobEgress{Workflow: name, Job: m[1]}
			inList = false
			continue
		}
		if current == nil {
			continue
		}
		if m := policyLine.FindStringSubmatch(line); m != nil {
			current.Policy = m[1]
			inList = false
			continue
		}
		if strings.Contains(strings.TrimSpace(line), "allowed-endpoints:") {
			inList = true
			continue
		}
		if inList {
			if m := bareHost.FindStringSubmatch(line); m != nil {
				current.Hosts = append(current.Hosts, m[1])
				continue
			}
			if strings.TrimSpace(line) == "" {
				continue
			}
			inList = false
		}
	}
	flush()
	return out
}

// declarations pulls the `egress:` section out of the suppliers list.
//
// It stops at the next top-level key, so a section added after it cannot be
// read as more entries.
func declarations(body string) []declaredEgress {
	var out []declaredEgress
	var current *declaredEgress
	inSection, inHosts := false, false

	flush := func() {
		if current != nil {
			out = append(out, *current)
			current = nil
		}
	}

	for _, line := range strings.Split(body, "\n") {
		if topLevelKey.MatchString(line) {
			if !strings.HasPrefix(line, "egress:") {
				flush()
				inSection = false
				continue
			}
			inSection = true
			continue
		}
		if !inSection {
			continue
		}
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "- job:"):
			flush()
			current = &declaredEgress{Key: strings.TrimSpace(strings.TrimPrefix(t, "- job:"))}
			inHosts = false
		case current == nil:
			// a comment or blank line before the first entry
		case strings.HasPrefix(t, "policy:"):
			current.Policy = strings.TrimSpace(strings.TrimPrefix(t, "policy:"))
			inHosts = false
		case strings.HasPrefix(t, "reason:"):
			inHosts = false
		case strings.HasPrefix(t, "hosts:"):
			inHosts = true
		case inHosts:
			if m := hostEntry.FindStringSubmatch(line); m != nil {
				current.Hosts = append(current.Hosts, m[1])
			} else if t != "" {
				inHosts = false
			}
		}
	}
	flush()
	return out
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
	declared := declarations(string(suppliers))
	if len(declared) == 0 {
		return refuseEgress(fmt.Sprintf(
			"%s declares no egress at all. Either the 'egress:' section was removed - which "+
				"would turn this guard into a no-op while looking like a passing check - or "+
				"this is not the suppliers list.", suppliersPath))
	}

	entries, err := os.ReadDir(filepath.Join(*root, workflowsDir))
	if err != nil {
		return refuseEgress(fmt.Sprintf("cannot read %s: %v", workflowsDir, err))
	}

	var found []jobEgress
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(*root, workflowsDir, e.Name()))
		if err != nil {
			return refuseEgress(fmt.Sprintf("cannot read %s: %v", e.Name(), err))
		}
		found = append(found, workflowEgress(e.Name(), string(body))...)
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

	fmt.Printf("every one of the %d jobs in %d workflows reaches only what the suppliers list declares\n",
		len(found), len(entries))
	return 0
}

func refuseEgress(msg string) int {
	fmt.Fprintln(os.Stderr, "security guard-egress: "+msg)
	return 1
}
