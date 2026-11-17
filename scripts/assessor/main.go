// Command assessor measures how the agent's pull requests fared against this
// repository's checks, from the GitHub API and the git history, and writes
// the report with the queries behind every number.
//
// It holds a read-only token and changes nothing. Whatever it cannot measure
// it counts and leaves out; it estimates nothing.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"sort"
	"sync"
	"time"

	"homelab/details/ghapi"
)

func main() { os.Exit(run(os.Args[1:], os.Getenv)) }

// run is the program, given its arguments and its environment, returning the
// exit status.
func run(args []string, env func(string) string) int {
	flags := flag.NewFlagSet("assessor", flag.ContinueOnError)
	var o options
	flags.StringVar(&o.Repo, "repo", env("GITHUB_REPOSITORY"), "owner/name of the repository")
	flags.StringVar(&o.Agent, "agent", "", "login of the account whose pull requests are measured")
	flags.StringVar(&o.Rev, "rev", "origin/main", "revision whose history and guards are read")
	flags.StringVar(&o.Dir, "dir", ".", "the git checkout")
	flags.StringVar(&o.Cache, "cache", "", "directory that keeps API answers between runs")
	flags.StringVar(&o.Out, "out", "", "file the report is written to")
	flags.StringVar(&o.Gate, "gate", "", "check that waits for a person")
	flags.StringVar(&o.Guards, "guards", "", "directory of the Go guards")
	flags.StringVar(&o.Rules, "rules", "", "directory of the Conftest rules")
	flags.StringVar(&o.Guidance, "guidance", "", "file whose edits are the timeline")
	flags.IntVar(&o.Window, "window", 20, "pull requests compared each side of an edit")
	flags.StringVar(&o.Base, "api", "", "GitHub's API address; empty is GitHub itself")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if o.Repo == "" || o.Agent == "" || o.Cache == "" || o.Out == "" ||
		o.Gate == "" || o.Guards == "" || o.Rules == "" || o.Guidance == "" {
		fmt.Fprintln(os.Stderr, "assessor: -repo, -agent, -cache, -out, -gate, -guards, -rules and -guidance are all required")
		return 2
	}
	token := env("GH_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "assessor: GH_TOKEN is empty; the report needs a token that can read the repository")
		return 2
	}
	err := os.MkdirAll(o.Cache, 0o700)
	var r *report
	if err == nil {
		c := &client{base: o.Base, repo: o.Repo, token: token, cache: o.Cache}
		r, err = measure(c, history{o.Dir, o.Rev}, o)
	}
	if err == nil {
		err = os.WriteFile(o.Out, []byte(render(r)), 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "assessor:", err)
		return 1
	}
	return 0
}

type options struct {
	Repo, Agent, Rev, Dir, Cache, Out, Gate, Guards, Rules, Guidance, Base string
	Window                                                                 int
}

// A month of first pushes, judged four ways.
type month struct {
	Name                                      string
	All, AllButGate, Required, RequiredNoGate tally
	SameSpan                                  tally
	Edits, Repeats                            int
}

// report is everything the written report says.
type report struct {
	Options              options
	Commit               string
	From, To             time.Time
	Pulls, AgentPulls    int
	NoRun                int
	Months               []*month
	RequiredFrom         time.Time
	FailedRuns           int
	FailedJobs, GateEcho int
	LogsGone             int
	Families             map[string]int
	Findings             map[finding]int
	Guards               int
	Incidents            []incident
	Edits                int
	Compared             int
	Better, Worse, Same  int
	Changes              []float64
}

// pullFor is the pull request a run was for: the newest of the agent's pull
// requests on the run's branch opened before the run.
func pullFor(pulls []pull, agent string, r workflowRun) pull {
	var found pull
	for _, p := range pulls {
		if p.User.Login == agent && p.Head.Ref == r.HeadBranch &&
			!p.CreatedAt.After(r.CreatedAt.Add(2*time.Minute)) && p.CreatedAt.After(found.CreatedAt) {
			found = p
		}
	}
	return found
}

// touched is every file a pull request changed.
func touched(c *client, number int) ([]string, error) {
	files, err := pages[struct {
		Filename string `json:"filename"`
	}](c, fmt.Sprintf("/pulls/%d/files", number), "")
	var names []string
	for _, f := range files {
		names = append(names, f.Filename)
	}
	return names, err
}

// measure reads the API and the history and computes the report.
func measure(c *client, h history, o options) (*report, error) {
	r := &report{Options: o, Families: map[string]int{}, Findings: map[finding]int{}}
	commit, err := h.git("rev-parse", "--short", h.rev)
	if err != nil {
		return nil, err
	}
	r.Commit = commit[:len(commit)-1]

	pulls, err := pages[pull](c, "/pulls?state=all&sort=created&direction=asc", "")
	if err != nil {
		return nil, err
	}
	runs, err := pages[workflowRun](c, ghapi.WorkflowRuns, "workflow_runs")
	if err != nil {
		return nil, err
	}
	history, err := h.requirements()
	if err != nil {
		return nil, err
	}
	if len(history) > 0 {
		r.RequiredFrom = history[0].From
	}

	// First pushes, and what each got.
	type first struct {
		pull pull
		sha  string
		at   time.Time
	}
	var firsts []first
	branches := map[string]time.Time{}
	r.Pulls = len(pulls)
	for _, p := range pulls {
		if p.User.Login != o.Agent {
			continue
		}
		r.AgentPulls++
		branches[p.Head.Ref] = p.CreatedAt
		sha, at, ok := firstPush(p, runs)
		if !ok {
			r.NoRun++
			continue
		}
		firsts = append(firsts, first{p, sha, at})
	}
	months := map[string]*month{}
	monthOf := func(at time.Time) *month {
		name := at.UTC().Format("2006-01")
		if months[name] == nil {
			months[name] = &month{Name: name}
			r.Months = append(r.Months, months[name])
		}
		return months[name]
	}
	var lock sync.Mutex
	var judged []measured
	err = each(firsts, func(f first) error {
		checks, err := pages[check](c, "/commits/"+f.sha+"/check-runs?filter=all", "check_runs")
		if err != nil {
			return err
		}
		lock.Lock()
		defer lock.Unlock()
		m := monthOf(f.pull.CreatedAt)
		m.All.add(judge(checks, nil, ""))
		butGate := judge(checks, nil, o.Gate)
		m.AllButGate.add(butGate)
		judged = append(judged, measured{f.pull.CreatedAt, butGate})
		if required := requiredOn(history, f.at); required != nil {
			m.Required.add(judge(checks, required, ""))
			m.RequiredNoGate.add(judge(checks, required, o.Gate))
			m.SameSpan.add(butGate)
		}
		if r.From.IsZero() || f.pull.CreatedAt.Before(r.From) {
			r.From = f.pull.CreatedAt
		}
		if f.pull.CreatedAt.After(r.To) {
			r.To = f.pull.CreatedAt
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(judged, func(i, j int) bool { return judged[i].At.Before(judged[j].At) })

	// Every failed job of a pull-request run on one of the agent's branches.
	var failedRuns []workflowRun
	for _, run := range runs {
		if _, ours := branches[run.HeadBranch]; run.Event == "pull_request" && run.Conclusion == "failure" && ours {
			failedRuns = append(failedRuns, run)
		}
	}
	r.FailedRuns = len(failedRuns)
	var failures []failure
	err = each(failedRuns, func(run workflowRun) error {
		jobs, err := pages[job](c, fmt.Sprintf("/actions/runs/%d/jobs?filter=all", run.ID), "jobs")
		if err != nil {
			return err
		}
		for _, j := range jobs {
			if j.Conclusion != "failure" {
				continue
			}
			family := familyOf(j)
			log, err := c.get(fmt.Sprintf("/actions/jobs/%d/logs", j.ID))
			if err != nil && !errors.Is(err, errGone) {
				return err
			}
			lock.Lock()
			if family == "" {
				r.GateEcho++
			} else {
				r.FailedJobs++
				r.Families[family]++
			}
			if err != nil {
				r.LogsGone++
			}
			for _, f := range findings(string(log)) {
				r.Findings[f]++
				p := pullFor(pulls, o.Agent, run)
				failures = append(failures, failure{finding: f, At: run.CreatedAt, Opened: p.CreatedAt, pr: p.Number})
			}
			lock.Unlock()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Guards that cite an issue, and the issue each answers.
	guards, err := h.guards(o.Guards, o.Rules)
	if err != nil {
		return nil, err
	}
	r.Guards = len(guards)
	opened := map[int]time.Time{}
	for _, g := range guards {
		for _, n := range g.Issues {
			if _, seen := opened[n]; seen {
				continue
			}
			opened[n] = time.Time{}
			body, err := c.get(fmt.Sprintf("/issues/%d", n))
			if errors.Is(err, errGone) {
				continue
			} else if err != nil {
				return nil, err
			}
			// A pull request is an issue to this API too, told apart by a key
			// of its own; only an issue is an incident.
			var issue map[string]json.RawMessage
			var created time.Time
			if err := json.Unmarshal(body, &issue); err != nil {
				return nil, err
			}
			if err := json.Unmarshal(issue["created_at"], &created); err != nil {
				return nil, fmt.Errorf("issue %d: %w", n, err)
			}
			if _, isPull := issue["pull_request"]; !isPull {
				opened[n] = created
			}
		}
	}
	for n, at := range opened {
		if at.IsZero() {
			delete(opened, n)
		}
	}
	// Only a failure that could be a repeat needs to know what its pull
	// request changed, so only those ask.
	files := map[int][]string{}
	for i, f := range failures {
		candidate := false
		for _, g := range guards {
			candidate = candidate || f.Kind == g.Kind && f.Name == g.Name && f.Opened.After(g.Added)
		}
		if !candidate {
			continue
		}
		if _, seen := files[f.pr]; !seen {
			if files[f.pr], err = touched(c, f.pr); err != nil {
				return nil, err
			}
		}
		failures[i].Touched = files[f.pr]
	}
	r.Incidents = incidents(guards, opened, failures, []string{o.Guards, path.Dir(o.Rules)})
	for _, inc := range r.Incidents {
		for _, f := range failures {
			if repeats(f, inc.Guard) {
				monthOf(f.At).Repeats++
			}
		}
	}

	// The guidance file's edits, and the pull requests either side of each.
	edits, err := h.edits(o.Guidance)
	if err != nil {
		return nil, err
	}
	r.Edits = len(edits)
	for _, at := range edits {
		monthOf(at).Edits++
		before, after, ok := around(judged, at, o.Window)
		if !ok {
			continue
		}
		r.Compared++
		r.Changes = append(r.Changes, after-before)
		switch {
		case after > before:
			r.Better++
		case after < before:
			r.Worse++
		default:
			r.Same++
		}
	}
	sort.Slice(r.Months, func(i, j int) bool { return r.Months[i].Name < r.Months[j].Name })
	sort.Float64s(r.Changes)
	return r, nil
}
