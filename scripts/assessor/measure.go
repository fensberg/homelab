package main

import (
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// What the GitHub API returns, as far as the report reads it.
type pull struct {
	Number    int
	CreatedAt time.Time `json:"created_at"`
	ClosedAt  time.Time `json:"closed_at"`
	User      struct {
		Login string
	} `json:"user"`
	Head struct {
		Ref string `json:"ref"`
	} `json:"head"`
}

type workflowRun struct {
	ID         int64 `json:"id"`
	Event      string
	HeadBranch string    `json:"head_branch"`
	HeadSHA    string    `json:"head_sha"`
	CreatedAt  time.Time `json:"created_at"`
	Conclusion string    `json:"conclusion"`
}

type check struct {
	ID         int64
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
}

type job struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
	Steps      []struct {
		Name       string `json:"name"`
		Conclusion string `json:"conclusion"`
	} `json:"steps"`
}

// A verdict is what a pull request's first push got from its checks.
type verdict int

const (
	passed verdict = iota
	failed
	// unmeasured is a first push whose answer cannot be known: a check was
	// cancelled or never finished (a second push replaced it), no check ran,
	// or which checks were required that day is not on record.
	unmeasured
)

// firstPush is the commit a pull request was opened on: the commit of the
// earliest pull-request run on its branch while it was open. ok is false
// when no such run exists.
func firstPush(p pull, runs []workflowRun) (sha string, at time.Time, ok bool) {
	closed := p.ClosedAt
	if closed.IsZero() {
		closed = time.Now()
	}
	for _, r := range runs {
		if r.Event != "pull_request" || r.HeadBranch != p.Head.Ref {
			continue
		}
		if r.CreatedAt.Before(p.CreatedAt.Add(-2*time.Minute)) || r.CreatedAt.After(closed) {
			continue
		}
		if !ok || r.CreatedAt.Before(at) {
			sha, at, ok = r.HeadSHA, r.CreatedAt, true
		}
	}
	return sha, at, ok
}

// firstAttempts keeps, for each check name, its first attempt: the one
// GitHub numbered first, since a check run's number only grows. A re-run
// that passed does not make the first push a pass.
func firstAttempts(checks []check) map[string]check {
	first := map[string]check{}
	for _, c := range checks {
		if seen, ok := first[c.Name]; !ok || c.ID < seen.ID {
			first[c.Name] = c
		}
	}
	return first
}

// judge is the verdict of a first push over the checks that count. required
// nil means every check counts; otherwise only the named ones, and one of
// them not reporting is unmeasured. leaveOut is a check that waits for a
// person and is never the author's to pass.
func judge(checks []check, required []string, leaveOut string) verdict {
	first := firstAttempts(checks)
	delete(first, leaveOut)
	var counted []check
	if required == nil {
		for _, c := range first {
			counted = append(counted, c)
		}
	} else {
		for _, name := range required {
			if name == leaveOut {
				continue
			}
			c, ok := first[name]
			if !ok {
				c = check{Name: name, Conclusion: "missing"}
			}
			counted = append(counted, c)
		}
	}
	if len(counted) == 0 {
		return unmeasured
	}
	result := passed
	for _, c := range counted {
		switch c.Conclusion {
		case "success", "skipped", "neutral":
		case "failure", "timed_out", "startup_failure", "action_required":
			return failed
		default:
			result = unmeasured
		}
	}
	return result
}

// A requirement is the set of checks a merge was held to from a day on.
type requirement struct {
	From   time.Time
	Checks []string
}

// requiredOn is the set in force at a moment, or nil before the first one on
// record.
func requiredOn(history []requirement, at time.Time) []string {
	var set []string
	for _, r := range history {
		if !r.From.After(at) {
			set = r.Checks
		}
	}
	return set
}

// tally counts verdicts.
type tally struct{ Passed, Failed, Unmeasured int }

func (t *tally) add(v verdict) {
	switch v {
	case passed:
		t.Passed++
	case failed:
		t.Failed++
	default:
		t.Unmeasured++
	}
}

// rate is the share that passed among those measured, as a percentage, and
// false when none was measured.
func (t tally) rate() (float64, bool) {
	if t.Passed+t.Failed == 0 {
		return 0, false
	}
	return 100 * float64(t.Passed) / float64(t.Passed+t.Failed), true
}

// family names the kind of check a failed job belongs to, from the name
// GitHub shows for it. Lanes were renamed over the repository's life, so
// each family lists every name it has had. The first match wins.
var families = []struct {
	Name  string
	Match *regexp.Regexp
}{
	{"Acknowledgement gate (waits for a person)", regexp.MustCompile(`(?i)sensitive paths`)},
	{"Repository guards (Go, Conftest and Semgrep rules of the estate)", regexp.MustCompile(`(?i)invariants|repo policy|app removal`)},
	{"Mutation proofs of the guards", regexp.MustCompile(`(?i)meta-tests|mutation`)},
	{"Lint aggregate (Super-Linter, retired)", regexp.MustCompile(`(?i)super-linter`)},
	{"Plan of the infrastructure", regexp.MustCompile(`(?i)^plan|^sites$|converge`)},
	{"Unit tests and coverage floor", regexp.MustCompile(`(?i)^test \(|test / unit|frontend|vitest`)},
	{"Semgrep registry rules", regexp.MustCompile(`(?i)semgrep|scan / code`)},
	{"Formatting (pre-commit)", regexp.MustCompile(`(?i)format`)},
	{"Secret scan", regexp.MustCompile(`(?i)secrets`)},
	{"Spelling", regexp.MustCompile(`(?i)spelling`)},
	{"Vulnerabilities and misconfiguration (Trivy)", regexp.MustCompile(`(?i)trivy|scan / infrastructure`)},
	{"Dependency review", regexp.MustCompile(`(?i)dependency`)},
	{"Validate (OpenTofu, manifests, Go build)", regexp.MustCompile(`(?i)validate`)},
	{"Shell lint", regexp.MustCompile(`(?i)shell`)},
	{"Policy scan (Checkov)", regexp.MustCompile(`(?i)checkov|scan / policy`)},
	{"Workflow scan (zizmor)", regexp.MustCompile(`(?i)zizmor|scan / workflows`)},
	{"Dockerfile scan (hadolint)", regexp.MustCompile(`(?i)hadolint|scan / containers`)},
	{"Go vet", regexp.MustCompile(`(?i)vet`)},
	{"Reviewer (AI, retired)", regexp.MustCompile(`(?i)outside reading`)},
}

// familyOf is the family of a failed job, "" for a job that only repeats
// another's failure (the gate that waits on every lane), and "Other" for a
// name no family claims.
func familyOf(j job) string {
	var failedSteps []string
	for _, s := range j.Steps {
		if s.Conclusion == "failure" {
			failedSteps = append(failedSteps, s.Name)
		}
	}
	if j.Name == "tests passed" || len(failedSteps) == 1 && failedSteps[0] == "Every lane passed" {
		return ""
	}
	for _, f := range families {
		if f.Match.MatchString(j.Name) {
			return f.Name
		}
	}
	return "Other"
}

var (
	timestamp    = regexp.MustCompile(`(?m)^\d{4}-\d\d-\d\dT[\d:.]+Z ?`)
	colour       = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	failingTest  = regexp.MustCompile(`--- FAIL: (Test\w+)`)
	refusingRule = regexp.MustCompile(`FAIL - \S+ - \S+ - ([a-z0-9_]+):`)
	registryRule = regexp.MustCompile(`❯+❱ ?([A-Za-z0-9_.-]+)`)
	failedHook   = regexp.MustCompile(`(?m)^(.{3,70}?)\.{3,}Failed\s*$`)
)

// A finding is one thing a log says failed: its kind and its name.
type finding struct{ Kind, Name string }

// findings reads a failed job's log for what failed in it, each thing once.
func findings(log string) []finding {
	log = colour.ReplaceAllString(timestamp.ReplaceAllString(log, ""), "")
	seen := map[finding]bool{}
	for kind, pattern := range map[string]*regexp.Regexp{
		"Go test": failingTest, "Conftest rule": refusingRule,
		"Semgrep registry rule": registryRule, "pre-commit hook": failedHook,
	} {
		for _, m := range pattern.FindAllStringSubmatch(log, -1) {
			seen[finding{kind, strings.TrimSpace(m[1])}] = true
		}
	}
	out := make([]finding, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Kind+out[i].Name < out[j].Kind+out[j].Name
	})
	return out
}

// A guard is a check of the estate's own whose comment cites an issue.
type guard struct {
	Kind   string // "Go test" or "Conftest rule", as findings names them
	Name   string
	File   string
	Issues []int
	Added  time.Time
}

// An incident is a guard paired with the issue it answers: the newest issue
// its comment cites that was opened before the guard merged.
type incident struct {
	Guard    guard
	Issue    int
	Opened   time.Time
	Lead     time.Duration
	Repeats  int
	Plain    int
	LastSeen time.Time
}

// A failure is one finding in one failed job: when its run began, and when
// the pull request it ran for was opened.
type failure struct {
	finding
	At, Opened time.Time
	// Touched is every file the failing pull request changed.
	Touched []string
	pr      int
}

// incidents pairs each guard with its issue and counts how often the guard
// failed a pull request opened after it merged: the same mistake made again,
// and stopped. A pull request already open when the guard merged is left
// out, because it may be the guard's own follow-up work. A guard whose every
// cited issue is younger than it is has no pair.
func incidents(guards []guard, opened map[int]time.Time, failures []failure, roots []string) []incident {
	var out []incident
	for _, g := range guards {
		inc := incident{Guard: g}
		for _, n := range g.Issues {
			at, ok := opened[n]
			if ok && at.Before(g.Added) && at.After(inc.Opened) {
				inc.Issue, inc.Opened = n, at
			}
		}
		if inc.Issue == 0 {
			continue
		}
		inc.Lead = g.Added.Sub(inc.Opened)
		for _, f := range failures {
			if repeats(f, g) {
				inc.Repeats++
				if plainly(f, roots) {
					inc.Plain++
				}
				if f.At.After(inc.LastSeen) {
					inc.LastSeen = f.At
				}
			}
		}
		out = append(out, inc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Guard.Name < out[j].Guard.Name })
	return out
}

// repeats is whether a failure is the guard failing a pull request opened
// after the guard merged: the upper bound of a repeat.
func repeats(f failure, g guard) bool {
	return f.Kind == g.Kind && f.Name == g.Name && f.Opened.After(g.Added)
}

// plainly is whether a repeat came from a pull request that changed no
// check at all: no file under any of roots. A pull request rewriting the
// checks can trip a guard without making the guard's mistake, so only
// these are the lower bound.
func plainly(f failure, roots []string) bool {
	return !slices.ContainsFunc(f.Touched, func(file string) bool {
		return slices.ContainsFunc(roots, func(root string) bool {
			return strings.HasPrefix(file, root+"/")
		})
	})
}

// A measured pull request: when it was opened and what its first push got.
type measured struct {
	At      time.Time
	Verdict verdict
}

// around compares the pull requests opened just before a moment with those
// just after: the pass rate of the window measured ones on each side. ok is
// false when either side has fewer than window.
func around(prs []measured, at time.Time, window int) (before, after float64, ok bool) {
	var b, a []measured
	for _, p := range prs {
		if p.Verdict == unmeasured {
			continue
		}
		if p.At.Before(at) {
			b = append(b, p)
		} else {
			a = append(a, p)
		}
	}
	if len(b) < window || len(a) < window {
		return 0, 0, false
	}
	share := func(set []measured) float64 {
		n := 0
		for _, p := range set {
			if p.Verdict == passed {
				n++
			}
		}
		return 100 * float64(n) / float64(len(set))
	}
	return share(b[len(b)-window:]), share(a[:window]), true
}

// median is the middle of a set of durations, and zero for none.
func median(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}
