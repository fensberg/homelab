package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A pull request must never reach the self-hosted runner unattended.
//
// The self-hosted runner is a machine on the estate's own network, with the
// credentials to plan and apply against real infrastructure. Everything a
// pull request can run is supposed to be hermetic - that is the line
// tests/README.md draws, and the reason pr-validation.yml can run in full on
// a fork's pull request without exposing anything.
//
// deploy-infrastructure.yml's Plan job is the exception that proves the rule:
// it is pull_request-triggered, runs-on self-hosted, and reads real state. It
// carries a fork guard, which is necessary and not sufficient:
//
//	github.event.pull_request.head.repo.full_name == github.repository
//
// That excludes a fork. It does not exclude a branch pushed to this
// repository, which is a much larger set of people than it looks - every
// collaborator, every bot with push access, and anything holding a leaked
// token. Such a branch opens a pull request whose head repo IS this
// repository, sails past the guard, and executes on the runner.
//
// What closes that is the GitHub environment: `environment: staging` makes
// the job wait for a human when that environment carries required reviewers.
// So this test insists on both, because either alone leaves the runner
// reachable by somebody who should not reach it unattended.
//
// THE HALF THIS TEST CANNOT SEE: whether the environment actually has
// required reviewers configured. That lives in repository settings, not in
// the repository. Worse, naming an environment that does not exist is not an
// error - GitHub creates it on first use with no protection rules at all, so
// the file can look correct while the gate is wide open. The failure messages
// say so, because somebody satisfying this test by adding an `environment:`
// line and stopping there has not actually closed anything.

// forkGuardMarkers are the two halves of the comparison that excludes a
// fork's pull request. Both must be present: `head.repo.full_name` alone
// could be compared against anything, and `github.repository` appears in
// plenty of conditions that guard nothing.
var forkGuardMarkers = []string{
	"head.repo.full_name",
	"github.repository",
}

// eventPin matches an equality test against a specific triggering event.
var eventPin = regexp.MustCompile(`github\.event_name\s*(==|!=)\s*'([a-z_]+)'`)

// reachableFromPullRequest reports whether a job in a pull_request-triggered
// workflow can actually run on that event.
//
// A workflow's `on:` is not the whole answer. deploy-infrastructure.yml
// triggers on both pull_request and push, and its Apply job pins itself to
// the push half with `if: github.event_name == 'push'` - so it runs on the
// self-hosted runner but never from a pull request, and demanding a fork
// guard of it would be demanding a guard against something that cannot
// happen. The first draft of this test did exactly that, and this function is
// what it grew when the false positive showed up.
//
// Deliberately conservative: only an explicit pin excludes a job. Anything
// this cannot read - a variable, a call to a reusable workflow, an `if` with
// no event test at all - stays reachable and must carry the guards. Being
// wrong in that direction costs an argument in review; being wrong in the
// other direction costs the runner.
func reachableFromPullRequest(ifExpr string) bool {
	matches := eventPin.FindAllStringSubmatch(ifExpr, -1)
	if len(matches) == 0 {
		return true
	}
	for _, m := range matches {
		op, event := m[1], m[2]
		isPR := event == "pull_request" || event == "pull_request_target"
		// `!= 'pull_request'` excludes it; `== 'pull_request'` is exactly
		// the case that must be guarded.
		if op == "!=" && isPR {
			return false
		}
		if op == "==" && isPR {
			return true
		}
	}
	// Every pin names some other event, so the pull_request half of the
	// trigger cannot select this job.
	return false
}

type workflowFile struct {
	On   yaml.Node              `yaml:"on"`
	Jobs map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	RunsOn      yaml.Node `yaml:"runs-on"`
	If          string    `yaml:"if"`
	Environment yaml.Node `yaml:"environment"`
}

// triggerNames flattens the three shapes `on:` is allowed to take - a scalar,
// a sequence of event names, or a mapping of event name to filters.
func triggerNames(n *yaml.Node) []string {
	var out []string
	switch n.Kind {
	case yaml.ScalarNode:
		out = append(out, n.Value)
	case yaml.SequenceNode:
		for _, c := range n.Content {
			out = append(out, c.Value)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			out = append(out, n.Content[i].Value)
		}
	}
	return out
}

// isSelfHosted reports whether a runs-on selects a runner this project owns.
//
// `runs-on` may be a scalar, a list of labels, or a mapping with a `group`.
// A runner group is by definition a set of self-hosted runners, so the
// mapping form counts even when no label spells "self-hosted".
func isSelfHosted(n *yaml.Node) bool {
	name := scaleSetName()
	switch n.Kind {
	case yaml.ScalarNode:
		return n.Value == name
	case yaml.SequenceNode:
		for _, c := range n.Content {
			if c.Value == name {
				return true
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == "group" {
				return true
			}
			if n.Content[i].Value == "labels" && isSelfHosted(n.Content[i+1]) {
				return true
			}
		}
	}
	return false
}

// auditWorkflow returns one finding per violation, plus the number of
// pull_request-reachable self-hosted jobs it examined.
//
// Separated from the walk so the rules can be exercised against synthetic
// workflows below. A guard test that has only ever been run against a
// repository that passes it has not been shown to catch anything.
func auditWorkflow(rel string, body []byte) (findings []string, examined int, err error) {
	var wf workflowFile
	if err := yaml.Unmarshal(body, &wf); err != nil {
		return nil, 0, fmt.Errorf("parsing %s: %w", rel, err)
	}

	var onPR, onPRTarget bool
	for _, trigger := range triggerNames(&wf.On) {
		switch trigger {
		case "pull_request":
			onPR = true
		case "pull_request_target":
			onPRTarget = true
		}
	}
	if !onPR && !onPRTarget {
		return nil, 0, nil
	}

	// Map iteration is random; findings are sorted at the end so a failure
	// message reads the same on every run.
	for name, job := range wf.Jobs {
		if !isSelfHosted(&job.RunsOn) || !reachableFromPullRequest(job.If) {
			continue
		}
		examined++

		// pull_request_target runs the base repository's workflow with full
		// secrets while checking out the contributor's code. On a self-hosted
		// runner that is remote code execution on the estate's own network,
		// and no `if` makes it safe.
		if onPRTarget {
			findings = append(findings, fmt.Sprintf(`%s: job %q is triggered by pull_request_target and runs on the self-hosted runner.

pull_request_target grants secrets to a workflow evaluating an outside
contributor's branch. Pointed at a machine on the estate's network, that is
not a gate with a weakness - it is remote code execution by design. Use
pull_request, which runs without secrets from a fork.`, rel, name))
			continue
		}

		missingGuard := false
		for _, marker := range forkGuardMarkers {
			if !strings.Contains(job.If, marker) {
				missingGuard = true
			}
		}
		if missingGuard {
			findings = append(findings, fmt.Sprintf("%s: job %q runs on the self-hosted runner from a pull request with no fork guard.\n\n"+
				"Its `if:` must compare the head repository against this one, so an outside\n"+
				"contributor's branch cannot execute on a machine holding real credentials:\n\n"+
				"    github.event.pull_request.head.repo.full_name == github.repository", rel, name))
		}

		if job.Environment.IsZero() {
			findings = append(findings, fmt.Sprintf(`%s: job %q runs on the self-hosted runner from a pull request with no environment.

The fork guard alone is not enough. It excludes a fork; it does not exclude a
branch pushed to this repository, and that set includes every collaborator,
every bot with push access, and anything holding a leaked token. Such a
branch's pull request passes the fork guard and executes on the runner.

A GitHub environment with required reviewers is what makes that wait for a
human. Note that adding this line is only half the fix: an environment named
here but not configured in repository settings is created on first use with
NO protection rules, so the gate has to be confirmed in Settings ->
Environments as well.`, rel, name))
		}
	}
	sort.Strings(findings)
	return findings, examined, nil
}

func TestNoPullRequestJobReachesTheSelfHostedRunnerUnguarded(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, ".github", "workflows")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	workflows, examined := 0, 0
	for _, e := range entries {
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".yml") && !strings.HasSuffix(e.Name(), ".yaml")) {
			continue
		}
		rel := filepath.Join(".github", "workflows", e.Name())
		body, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		if readErr != nil {
			t.Fatalf("reading %s: %v", rel, readErr)
		}
		findings, n, auditErr := auditWorkflow(rel, body)
		if auditErr != nil {
			t.Fatalf("%v", auditErr)
		}
		workflows++
		examined += n
		for _, f := range findings {
			t.Error(f)
		}
	}

	// Two ways this could pass by doing nothing: the directory moved, or the
	// one job it was written for stopped being reachable. Both mean the check
	// has quietly stopped checking.
	if workflows < 5 {
		t.Fatalf("only %d workflows were parsed; this test is reading the wrong directory", workflows)
	}
	if examined == 0 {
		t.Fatal(`no pull_request-triggered job runs on the self-hosted runner.

That is a safer repository than the one this test was written for, but it also
means the assertions above ran against nothing. If the Plan job in
deploy-infrastructure.yml was deliberately moved off the self-hosted runner,
delete this test with it rather than leaving it passing vacuously.`)
	}
}

// --- the rules themselves ---------------------------------------------------

func TestAuditWorkflowCatchesTheWaysInAndIgnoresTheOthers(t *testing.T) {
	cases := []struct {
		name     string
		yaml     string
		findings int
		examined int
	}{
		{
			name: "guarded and gated is the shape we want",
			yaml: `
on: {pull_request: null}
jobs:
  plan:
    runs-on: ` + scaleSetName() + `
    environment: staging
    if: github.event.pull_request.head.repo.full_name == github.repository
`,
			findings: 0, examined: 1,
		},
		{
			// The case that motivated this test: a fork guard is not a
			// same-repo-branch guard, and this repository's bot can push one.
			name: "fork guard without an environment is still reachable",
			yaml: `
on: {pull_request: null}
jobs:
  plan:
    runs-on: ` + scaleSetName() + `
    if: github.event.pull_request.head.repo.full_name == github.repository
`,
			findings: 1, examined: 1,
		},
		{
			name: "no guard at all is two findings",
			yaml: `
on: {pull_request: null}
jobs:
  plan:
    runs-on: ` + scaleSetName() + `
`,
			findings: 2, examined: 1,
		},
		{
			// The false positive the first draft produced.
			name: "a push-pinned job in a PR-triggered workflow is not reachable",
			yaml: `
on:
  pull_request: null
  push: {branches: [main]}
jobs:
  apply:
    runs-on: ` + scaleSetName() + `
    if: github.event_name == 'push'
`,
			findings: 0, examined: 0,
		},
		{
			name: "pull_request_target on self-hosted is refused outright",
			yaml: `
on: {pull_request_target: null}
jobs:
  plan:
    runs-on: ` + scaleSetName() + `
    environment: staging
    if: github.event.pull_request.head.repo.full_name == github.repository
`,
			findings: 1, examined: 1,
		},
		{
			name: "a hosted runner is nobody's business here",
			yaml: `
on: {pull_request: null}
jobs:
  lint:
    runs-on: ubuntu-latest
`,
			findings: 0, examined: 0,
		},
		{
			name: "self-hosted hidden in a label list is still self-hosted",
			yaml: `
on: {pull_request: null}
jobs:
  plan:
    runs-on: [` + scaleSetName() + `, linux, x64]
`,
			findings: 2, examined: 1,
		},
		{
			name: "a runner group is self-hosted by definition",
			yaml: `
on: {pull_request: null}
jobs:
  plan:
    runs-on:
      group: estate
`,
			findings: 2, examined: 1,
		},
		{
			name: "a workflow no pull request can trigger is out of scope",
			yaml: `
on: {schedule: [{cron: "0 4 * * *"}]}
jobs:
  test:
    runs-on: ` + scaleSetName() + `
`,
			findings: 0, examined: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings, examined, err := auditWorkflow("synthetic.yml", []byte(tc.yaml))
			if err != nil {
				t.Fatalf("auditing: %v", err)
			}
			if len(findings) != tc.findings {
				t.Errorf("got %d findings, want %d:\n%s", len(findings), tc.findings, strings.Join(findings, "\n---\n"))
			}
			if examined != tc.examined {
				t.Errorf("examined %d jobs, want %d", examined, tc.examined)
			}
		})
	}
}

func TestReachableFromPullRequest(t *testing.T) {
	cases := []struct {
		ifExpr string
		want   bool
	}{
		{"", true},
		{"github.event_name == 'push'", false},
		{"github.event_name == 'pull_request'", true},
		{"github.event_name != 'pull_request'", false},
		{"github.event_name == 'push' || github.event_name == 'pull_request'", true},
		// No event test at all, so this says nothing about reachability -
		// conservative means reachable.
		{"github.event.pull_request.head.repo.full_name == github.repository", true},
		{"inputs.run_it == 'yes'", true},
	}
	for _, tc := range cases {
		if got := reachableFromPullRequest(tc.ifExpr); got != tc.want {
			t.Errorf("reachableFromPullRequest(%q) = %v, want %v", tc.ifExpr, got, tc.want)
		}
	}
}

// scaleSetName reads the runner scale set's name out of the manifest that
// declares it, rather than hard-coding it here.
//
// The guard above decides whether a job reaches the estate's own runner, and
// it used to compare against the literal "self-hosted". Renaming the scale set
// - which had to happen, because self-hosted is a reserved label that scale
// sets are never offered jobs for - would have left this matching a string
// nothing uses, so every job would have looked hermetic and the guard would
// have protected nothing while still passing.
func scaleSetName() string {
	root, err := os.Getwd()
	if err != nil {
		return ""
	}
	for i := 0; i < 5; i++ {
		p := filepath.Join(root, "clusters", "management", "infrastructure", "configs", "runner-scale-set.yaml")
		if body, err := os.ReadFile(p); err == nil {
			m := scaleSetNamePattern.FindStringSubmatch(string(body))
			if m != nil {
				return m[1]
			}
			return ""
		}
		root = filepath.Dir(root)
	}
	return ""
}

var scaleSetNamePattern = regexp.MustCompile(`(?m)^\s*runnerScaleSetName:\s*(\S+)\s*$`)
