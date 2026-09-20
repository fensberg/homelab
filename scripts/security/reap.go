package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"
)

// reap-queue cancels queued runs that can never produce a useful result.
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
func reapQueue(args []string) int {
	fs := flag.NewFlagSet("reap-queue", flag.ExitOnError)
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
	return reapWith(&client{repo: *repo, token: token}, *confirm)
}

// reapWith is the verb itself, with the client handed in so the decisions -
// what is a candidate, what is proof, what a dry run does - can be exercised
// against a stub rather than against GitHub.
func reapWith(c *client, confirm bool) int {
	var candidates []run
	for status := range stuckStatuses {
		runs, err := c.runs("", "status="+status)
		if err != nil {
			fmt.Println("could not ask GitHub for", status, "runs:", err)
			return 1
		}
		candidates = append(candidates, runs...)
	}
	// A person is not a fault and is not reaped.
	_, candidates = waitingOnAPerson(candidates, c.awaitingApproval)

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })

	var dead []run
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
func (c *client) whyDead(r run) (string, bool, error) {
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
func (c *client) branchExists(branch string) (bool, error) {
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

func (c *client) head(url string) (int, error) {
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
func (c *client) cancel(id int64) error {
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
