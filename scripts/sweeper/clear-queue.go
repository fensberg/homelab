package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"time"
)

// clear-queue cancels queued runs that can never produce a useful result.
//
// A queued run does not die with its branch. Two plans on a branch that had
// been merged and deleted sat in the queue until an ignition refused to start
// because of them - at the one moment when being stopped is most expensive
// (#181). The runner is a pod inside the cluster, so a run queued against it
// waits indefinitely and then acquires it the moment one exists, at a commit
// from a branch nobody has any more.
//
// WHAT IT WILL CANCEL, AND WHY THE TEST IS EXACT RATHER THAN A JUDGEMENT.
// Only runs that are provably dead: the head branch no longer exists, or the
// pull request that produced them is closed. Neither can ever produce a result
// anybody wants. Everything else is left alone, including a run merely waiting
// a long time - "this has been queued a while" is a symptom with several
// causes, and the patrol reports it rather than acting on it.
//
// A run held at an environment approval is never touched. It is waiting for a
// person, which is the system working.
func clearQueue(args []string) int {
	fs := flag.NewFlagSet("clear-queue", flag.ExitOnError)
	var (
		repo    = fs.String("repo", envOr("GITHUB_REPOSITORY", ""), "owner/name to inspect")
		confirm = fs.Bool("confirm", false, "actually cancel; without this it only reports")
	)
	_ = fs.Parse(args)

	if *repo == "" {
		fatal("no repository given: pass -repo owner/name or set GITHUB_REPOSITORY")
	}
	token := envOr("GITHUB_TOKEN", "")
	if token == "" {
		fatal("GITHUB_TOKEN is empty; nothing can be asked or cancelled")
	}
	return clearWith(&github{repo: *repo, token: token}, *confirm)
}

// reapWith is the verb itself, with the client handed in so the decisions -
// what is a candidate, what is proof, what a dry run does - can be exercised
// against a stub rather than against GitHub.
func clearWith(c *github, confirm bool) int {
	var candidates []workflowRun
	for _, status := range []string{"queued", "pending"} {
		runs, err := c.runs(status)
		if err != nil {
			fmt.Println("could not ask GitHub for", status, "runs:", err)
			return 1
		}
		candidates = append(candidates, runs...)
	}
	// A person is not a fault and is not reaped.
	_, candidates = waitingOnAPerson(candidates, c.awaitingApproval)

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })

	var dead []workflowRun
	for _, r := range candidates {
		why, isDead, err := c.whyDead(r)
		if err != nil {
			fmt.Printf("  #%d (%s): could not establish whether it is dead (%v), leaving it\n", r.ID, r.Branch, err)
			continue
		}
		if !isDead {
			continue
		}
		dead = append(dead, r)
		fmt.Printf("  #%d (%s, queued %s): %s\n", r.ID, r.Branch,
			time.Since(r.CreatedAt).Round(time.Minute), why)
	}

	if len(dead) == 0 {
		fmt.Println("no queued run is provably dead")
		return 0
	}
	if !confirm {
		fmt.Printf("\n%d run(s) would be cancelled. Re-run with -confirm to do it.\n", len(dead))
		return 0
	}
	for _, r := range dead {
		if err := c.cancel(r.ID); err != nil {
			fmt.Printf("  #%d: cancel refused: %v\n", r.ID, err)
			return 1
		}
		fmt.Printf("  #%d: cancelled\n", r.ID)
	}
	return 0
}

// whyDead reports whether this run can still produce a useful result, and says
// why not when it cannot.
func (c *github) whyDead(r workflowRun) (string, bool, error) {
	if r.Branch == "" {
		return "", false, nil
	}
	exists, err := c.branchExists(r.Branch)
	if err != nil {
		return "", false, err
	}
	why, dead := deadBecause(exists)
	return why, dead, nil
}

// deadBecause is the judgement itself, kept apart from the asking so it can be
// read and tested without a network.
//
// Deliberately narrow: a missing branch is proof that no result from this run
// can be wanted. "Queued a long time" is not - that is a symptom with several
// causes, and the patrol reports it rather than acting on it.
func deadBecause(branchExists bool) (string, bool) {
	if !branchExists {
		return "its branch no longer exists", true
	}
	return "", false
}

// branchExists asks whether the run's head branch is still in the repository.
func (c *github) branchExists(branch string) (bool, error) {
	status, err := c.head(c.endpoint("/repos/%s/branches/%s", c.repo, branch))
	if err != nil {
		return false, err
	}
	switch status {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("GitHub answered %d", status)
	}
}

func (c *github) head(url string) (int, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// cancel stops one run. GitHub answers 202 when it accepts the request and 409
// when the run has already finished, which is not a failure worth reporting -
// the run is no longer queued either way.
func (c *github) cancel(id int64) error {
	req, err := http.NewRequest(http.MethodPost,
		c.endpoint("/repos/%s/actions/runs/%d/cancel", c.repo, id), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusAccepted, http.StatusConflict:
		return nil
	default:
		var answer struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &answer)
		return fmt.Errorf("GitHub answered %d: %s", resp.StatusCode, answer.Message)
	}
}

// A small GitHub client, kept here rather than shared with security's patrol.
//
// The two modules are deliberately separate programs with no dependency
// between them - one refuses things and the other takes finished things away -
// and neither has a third-party dependency. Sharing forty lines of HTTP would
// mean a library module between them, which is more machinery than the
// duplication costs. What must not drift is the JUDGEMENT, and that lives here
// alone: the patrol reports a queue that is not moving, and never cancels.
type github struct {
	repo  string
	token string
	// api is where GitHub is, empty everywhere but in tests. The statuses are
	// most of what can go wrong here - a 404 meaning a branch is gone, a 409
	// meaning the run finished while we were deciding - and they cannot be
	// exercised at all while the address is a constant.
	api string
}

type workflowRun struct {
	ID        int64     `json:"id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	Branch    string    `json:"head_branch"`
}

func (c *github) endpoint(format string, args ...any) string {
	base := c.api
	if base == "" {
		base = "https://api.github.com"
	}
	return base + fmt.Sprintf(format, args...)
}

func (c *github) get(url string) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return body, resp.StatusCode, err
}

// runs asks for one page of runs in a given status.
func (c *github) runs(status string) ([]workflowRun, error) {
	body, code, err := c.get(c.endpoint("/repos/%s/actions/runs?per_page=100&status=%s", c.repo, status))
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		// The body can carry a token; report the status only.
		return nil, fmt.Errorf("GitHub answered %d", code)
	}
	var page struct {
		Runs []workflowRun `json:"workflow_runs"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, err
	}
	return page.Runs, nil
}

// awaitingApproval says whether a person is what this run is waiting for.
//
// An error is reported rather than swallowed: "I could not ask" is not "nobody
// is waiting", and a run in that state is left alone rather than cancelled.
func (c *github) awaitingApproval(id int64) (bool, error) {
	body, code, err := c.get(c.endpoint("/repos/%s/actions/runs/%d/pending_deployments", c.repo, id))
	if err != nil {
		return false, err
	}
	if code != http.StatusOK {
		return false, fmt.Errorf("GitHub answered %d", code)
	}
	var pending []struct {
		Environment struct {
			Name string `json:"name"`
		} `json:"environment"`
	}
	if err := json.Unmarshal(body, &pending); err != nil {
		return false, err
	}
	return len(pending) > 0, nil
}

// waitingOnAPerson splits runs into those a human is holding and the rest.
func waitingOnAPerson(runs []workflowRun, ask func(int64) (bool, error)) (held, rest []workflowRun) {
	for _, r := range runs {
		if waiting, err := ask(r.ID); err == nil && waiting {
			held = append(held, r)
			continue
		}
		rest = append(rest, r)
	}
	return held, rest
}

// envOr reads an environment variable with a fallback.
func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "sweeper:", msg)
	os.Exit(2)
}
