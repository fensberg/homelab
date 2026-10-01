package repo

import (
	"fmt"
	"homelab/details/repopath"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A pull request's code never runs on the estate's runner (#554).
//
// The management pool is a machine on the estate's own network, holding the
// credentials that converge real infrastructure. Everything a pull request
// can run is supposed to reach nothing of value - that is what makes the
// pull request's approval the only gate this estate needs.
//
// It used to be enforced the other way round. The estate's plan ran pull
// request code on that runner, so this test demanded a fork guard and an
// environment on every such job, and a required reviewer on the environment
// was what made a branch pushed to this repository wait for a human. Three
// things were wrong with that: it put a click on every plan; this test could
// never see whether the environment actually had a reviewer, and one named
// here without one was open (staging was); and it made every collaborator,
// bot and leaked token one unreviewed branch away from the vault token.
//
// Plans now run against the as-built record on GitHub's runner and hold no
// credential, so the rule can be the plain one: a pull-request job runs on a
// GitHub-hosted runner, or on a pool declared here as the pull-request pool -
// one with no secret and no route to the estate. Anything else is refused,
// the management pool by name and any runner this cannot identify by
// default, because a label nobody declared is a runner nobody vetted.

// pullRequestPools are self-hosted pools built for pull-request work: no
// secret, and egress that cannot reach the estate. None exists yet; the pool
// that brings PR Validation onto the estate's compute is declared here when
// it does, and not before its network policy is.
var pullRequestPools = map[string]bool{}

// hostedLabel is a runner GitHub provides.
var hostedLabel = regexp.MustCompile(`^(ubuntu|windows|macos)-[a-z0-9.-]+$`)

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

// pullRequestSafe reports whether a runs-on names only runners a pull
// request may use: a GitHub-hosted one, or a declared pull-request pool. A
// runner group, an expression, or any label not known is not safe, because
// nothing here can say what it reaches.
func pullRequestSafe(n *yaml.Node) bool {
	safe := func(label string) bool { return hostedLabel.MatchString(label) || pullRequestPools[label] }
	switch n.Kind {
	case yaml.ScalarNode:
		return safe(n.Value)
	case yaml.SequenceNode:
		if len(n.Content) == 0 {
			return false
		}
		for _, c := range n.Content {
			if !safe(c.Value) {
				return false
			}
		}
		return true
	}
	return false
}

// auditWorkflow returns one finding per pull-request job on a runner a pull
// request may not use, plus the number of pull-request jobs it examined.
//
// Separated from the walk so the rule can be exercised against synthetic
// workflows below. A guard that has only ever been run against a repository
// that passes it has not been shown to catch anything.
func auditWorkflow(rel string, body []byte) (findings []string, examined int, err error) {
	var wf workflowFile
	if err := yaml.Unmarshal(body, &wf); err != nil {
		return nil, 0, fmt.Errorf("parsing %s: %w", rel, err)
	}

	onPR := false
	for _, trigger := range triggerNames(&wf.On) {
		if trigger == "pull_request" || trigger == "pull_request_target" {
			onPR = true
		}
	}
	if !onPR {
		return nil, 0, nil
	}

	for name, job := range wf.Jobs {
		if !reachableFromPullRequest(job.If) {
			continue
		}
		examined++
		if pullRequestSafe(&job.RunsOn) {
			continue
		}
		runsOn := job.RunsOn.Value
		if runsOn == "" {
			runsOn = "a list, a group or an expression"
		}
		findings = append(findings, fmt.Sprintf(`%s: job %q runs pull-request code on %s.

A pull request's code runs on a GitHub-hosted runner, or on a pool declared in
pullRequestPools: one with no secret and no route to the estate. The
management pool (%s) converges real infrastructure and is reached from main
only. A fork guard or an environment does not change that: a branch pushed to
this repository passes the first, and an environment is only a gate if
somebody remembers to give it a reviewer - which is the approval this estate
no longer asks for.`, rel, name, runsOn, scaleSetName()))
	}
	sort.Strings(findings)
	return findings, examined, nil
}

func TestNoPullRequestJobRunsOnTheEstatesRunner(t *testing.T) {
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
		t.Fatal("no pull-request job was found at all, so this checked nothing: the workflows moved, or the reader stopped matching them")
	}
	if scaleSetName() == "" {
		t.Fatal("the management pool's name could not be read from its manifest, so a finding could not name it")
	}
}

// --- the rules themselves ---------------------------------------------------

func TestAuditWorkflowCatchesTheWaysInAndIgnoresTheOthers(t *testing.T) {
	// One site's pool, by the name its runners register under.
	pool := strings.Replace(scaleSetName(), "${SITE}", "alpha", 1)
	cases := []struct {
		name     string
		yaml     string
		findings int
		examined int
	}{
		{
			name:     "a hosted runner is where pull-request code runs",
			yaml:     "on: {pull_request: null}\njobs:\n  plan:\n    runs-on: ubuntu-latest\n",
			findings: 0, examined: 1,
		},
		{
			// The shape this estate used to require, and the reason it no
			// longer counts: both guards present, and still the estate's runner.
			name: "guarded and gated on the management pool is still refused",
			yaml: `
on: {pull_request: null}
jobs:
  plan:
    runs-on: ` + pool + `
    environment: management
    if: github.event.pull_request.head.repo.full_name == github.repository
`,
			findings: 1, examined: 1,
		},
		{
			name: "a push-pinned job in a pull-request workflow is not reachable",
			yaml: `
on:
  pull_request: null
  push: {branches: [main]}
jobs:
  converge:
    runs-on: ` + pool + `
    if: github.event_name == 'push'
`,
			findings: 0, examined: 0,
		},
		{
			name:     "pull_request_target is held to the same rule",
			yaml:     "on: {pull_request_target: null}\njobs:\n  plan:\n    runs-on: " + pool + "\n",
			findings: 1, examined: 1,
		},
		{
			name:     "the management pool hidden in a label list",
			yaml:     "on: {pull_request: null}\njobs:\n  plan:\n    runs-on: [" + pool + ", linux]\n",
			findings: 1, examined: 1,
		},
		{
			name:     "a runner group cannot be identified, so it is refused",
			yaml:     "on: {pull_request: null}\njobs:\n  plan:\n    runs-on:\n      group: estate\n",
			findings: 1, examined: 1,
		},
		{
			name:     "a label nobody declared is a runner nobody vetted",
			yaml:     "on: {pull_request: null}\njobs:\n  plan:\n    runs-on: some-new-pool\n",
			findings: 1, examined: 1,
		},
		{
			name:     "an expression cannot be read, so it is refused",
			yaml:     "on: {pull_request: null}\njobs:\n  plan:\n    runs-on: ${{ inputs.runner }}\n",
			findings: 1, examined: 1,
		},
		{
			name:     "a workflow no pull request can trigger is out of scope",
			yaml:     "on: {schedule: [{cron: \"0 4 * * *\"}]}\njobs:\n  test:\n    runs-on: " + pool + "\n",
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

	// A pool declared for pull requests is allowed, which is how PR
	// Validation reaches the estate's compute without reaching the estate.
	pullRequestPools["estate-pull-requests"] = true
	defer delete(pullRequestPools, "estate-pull-requests")
	findings, _, _ := auditWorkflow("synthetic.yml", []byte("on: {pull_request: null}\njobs:\n  lint:\n    runs-on: estate-pull-requests\n"))
	if len(findings) != 0 {
		t.Errorf("a declared pull-request pool was refused: %v", findings)
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
	// Found by the release it declares, wherever the manifest is. No name is
	// an answer the callers refuse: a guard that could not read the name
	// would otherwise compare every job against nothing and pass.
	path, err := fluxObjectPath(kindHelmRelease, runnerScaleSet)
	if err != nil {
		return ""
	}
	root, err := repopath.Root()
	if err != nil {
		return ""
	}
	body, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		return ""
	}
	if m := scaleSetNamePattern.FindStringSubmatch(string(body)); m != nil {
		return m[1]
	}
	return ""
}

var scaleSetNamePattern = regexp.MustCompile(`(?m)^\s*runnerScaleSetName:\s*(\S+)\s*$`)
