// canary watches the estate from outside it.
//
// Everything else that reports on this estate runs inside it: the Health phase
// gates a converge, and epoch 04's monitoring will live in the cluster. All of
// that is blind to the failure that actually happened - the estate stopped
// being able to run jobs at all, and nothing said so for three days.
//
// The nightly drift check sat queued from 29 to 31 August. It never failed,
// because a job that never starts cannot fail: `timeout-minutes` only counts
// once a job is running, and GitHub cancels a stale queued run after about a
// day without telling anyone. The one check that would notice somebody editing
// a VM by hand in the hypervisor UI was silently absent, and the first symptom
// was a converge that would not run.
//
// So this deliberately runs on a GitHub-hosted runner, outside the estate,
// where the estate cannot starve it. It asks only questions answerable from
// outside, because that constraint is real: the cluster's API lives on the
// overlay network, and reaching it from here would mean putting a tailnet key
// at GitHub - a credential with network access to the estate, stored outside
// it. That trade is worse than the blindness it buys.
//
// It reports structure and never a value, the same line `plan` and
// `check-inventory` draw, because its output lands in a job summary that
// anyone can read.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// A check is one question with one answer. Each returns a short status line.
//
// Four answers, and the two that are not "ok" are not alike. "fail" is the
// estate being unhealthy. "unknown" is the patrol being unable to look - GitHub
// did not answer - and it counts against health exactly as a failure does,
// because a patrol that saw nothing must not print that everything is fine. It
// used to be a "skip", which did not count, so an unreachable API produced
// "the estate is answering for itself" over a patrol that had asked nothing.
// "skip" is kept for the one honest case: there is nothing to check yet.
type result struct {
	name   string
	status string // "ok", "fail", "unknown", "skip"
	detail string
}

// unhealthy counts the results that stand against the estate being healthy.
func unhealthy(results []result) int {
	var n int
	for _, r := range results {
		if r.status == "fail" || r.status == "unknown" {
			n++
		}
	}
	return n
}

func patrol(args []string) int {
	fs := flag.NewFlagSet("patrol", flag.ExitOnError)
	var (
		repo         = fs.String("repo", envOr("GITHUB_REPOSITORY", ""), "owner/name to inspect")
		queuedFor    = fs.Duration("max-queued", 30*time.Minute, "how long a run may sit queued before that is a fault")
		nightlyEvery = fs.Duration("nightly-within", 30*time.Hour, "the scheduled tier must have finished within this")
		nightly      = fs.String("nightly-workflow", "integration-tests.yml", "the workflow file whose scheduled runs are the drift check")
	)
	_ = fs.Parse(args)

	if *repo == "" {
		fatal("no repository given: pass -repo owner/name or set GITHUB_REPOSITORY")
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		fatal("GITHUB_TOKEN is empty; the canary cannot ask GitHub anything")
	}
	c := &client{repo: *repo, token: token}

	results := []result{
		c.noRunStuckInTheQueue(*queuedFor),
		c.scheduledTierIsActuallyRunning(*nightly, *nightlyEvery),
		c.lastConvergeDidNotFail(),
	}

	fmt.Println("estate canary")
	fmt.Println(strings.Repeat("-", 60))
	for _, r := range results {
		mark := map[string]string{"ok": "[ok]  ", "fail": "[FAIL]", "unknown": "[????]", "skip": "[skip]"}[r.status]
		fmt.Printf("%s %-34s %s\n", mark, r.name, r.detail)
	}
	fmt.Println(strings.Repeat("-", 60))
	failed := unhealthy(results)

	if failed > 0 {
		fmt.Printf("\n%d check(s) failed. The estate is not answering for itself.\n", failed)
		return 1
	}
	fmt.Println("\nthe estate is answering for itself")
	return 0
}

type client struct {
	repo  string
	token string
	// api is where GitHub is, and is empty everywhere but in tests. The HTTP
	// around these checks - a 404 meaning a branch is gone, a 409 meaning a
	// run already finished - is most of what can go wrong with them, and it
	// cannot be exercised at all while the address is a constant.
	api string
}

// endpoint builds a URL against GitHub, or against whatever a test stood up.
func (c *client) endpoint(format string, args ...any) string {
	base := c.api
	if base == "" {
		base = "https://api.github.com"
	}
	return base + fmt.Sprintf(format, args...)
}

type run struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Branch     string    `json:"head_branch"`
	Event      string    `json:"event"`
}

// runsURL is where a question about runs is asked. With a workflow file it is
// asked about that workflow only; without one, about the whole repository.
//
// The difference is load-bearing for the nightly check - see
// scheduledTierIsActuallyRunning - which is why it is a function with a test
// rather than a format string inline.
func runsURL(base, repo, workflow, query string) string {
	if base == "" {
		base = "https://api.github.com"
	}
	if workflow != "" {
		return fmt.Sprintf("%s/repos/%s/actions/workflows/%s/runs?per_page=100&%s", base, repo, workflow, query)
	}
	return fmt.Sprintf("%s/repos/%s/actions/runs?per_page=100&%s", base, repo, query)
}

// runs asks GitHub about runs: in one workflow when workflow is non-empty,
// across the repository when it is empty.
func (c *client) runs(workflow, query string) ([]run, error) {
	req, err := http.NewRequest(http.MethodGet, runsURL(c.api, c.repo, workflow, query), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		// The body can carry a token; report the status only.
		return nil, fmt.Errorf("GitHub answered %d", resp.StatusCode)
	}
	var page struct {
		Runs []run `json:"workflow_runs"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, err
	}
	return page.Runs, nil
}

// stuckStatuses are the states that mean nothing will start the work.
//
// `waiting` is deliberately absent: it means waiting for a person to approve a
// deployment environment, and GitHub already notifies whoever can.
//
// THAT EXCLUSION DID NOT WORK, and the reason is worth keeping. A run held at
// an environment approval does not reliably report `waiting` - it frequently
// reports `pending`, which is in this map - so the patrol went red for exactly
// the cause it meant to ignore. It did so again on 2026-09-20, with three
// deploy runs sitting on the management environment's reviewer.
//
// A status word cannot answer this. `pending_deployments` can: it is empty
// unless a person is the thing being waited on. So the status selects
// candidates and GitHub decides which of them are somebody's turn.
var stuckStatuses = map[string]bool{"queued": true, "pending": true}

// awaitingApproval asks whether this run is waiting for a human to approve a
// deployment environment.
//
// An error is reported rather than swallowed: "I could not ask" is not "nobody
// is waiting", and the caller keeps the run in its count rather than quietly
// dropping it.
func (c *client) awaitingApproval(id int64) (bool, error) {
	req, err := http.NewRequest(http.MethodGet,
		c.endpoint("/repos/%s/actions/runs/%d/pending_deployments", c.repo, id), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("GitHub answered %d", resp.StatusCode)
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
//
// Kept separate from stuck() so the counting stays pure and testable, and the
// asking stays in one place.
func waitingOnAPerson(runs []run, ask func(int64) (bool, error)) (held, rest []run) {
	for _, r := range runs {
		// An error means the question was not answered. Keep it in the count:
		// a patrol that drops what it could not ask about reports health it
		// did not establish.
		if waiting, err := ask(r.ID); err == nil && waiting {
			held = append(held, r)
			continue
		}
		rest = append(rest, r)
	}
	return held, rest
}

// stuck counts the runs nobody can start that have been waiting past the
// limit, and reports the longest.
func stuck(runs []run, now time.Time, limit time.Duration) (count int, worst time.Duration) {
	for _, r := range runs {
		if !stuckStatuses[r.Status] {
			continue
		}
		if age := now.Sub(r.CreatedAt); age > limit {
			count++
			if age > worst {
				worst = age
			}
		}
	}
	return count, worst
}

// The check that would have caught the runner deprecation, the dead listener
// before it, and the next cause nobody has met yet. It asks about the symptom
// rather than any one mechanism: work is not being picked up.
func (c *client) noRunStuckInTheQueue(limit time.Duration) result {
	const name = "no run stuck in the queue"

	var all []run
	for status := range stuckStatuses {
		runs, err := c.runs("", "status="+status)
		if err != nil {
			return result{name, "unknown", "could not ask GitHub: " + err.Error()}
		}
		all = append(all, runs...)
	}
	held, rest := waitingOnAPerson(all, c.awaitingApproval)
	count, worst := stuck(rest, time.Now(), limit)
	if count > 0 {
		detail := fmt.Sprintf(
			"%d run(s) queued longer than %s, oldest %s.\n"+
				"       Work is not being picked up. A job that never starts never fails:\n"+
				"       timeout-minutes only counts once it is running, so nothing else reports this.",
			count, limit.Round(time.Minute), worst.Round(time.Minute))
		if len(held) > 0 {
			detail += fmt.Sprintf("\n       (%d more are waiting on an approval, which is a person rather than a fault.)", len(held))
		}
		return result{name, "fail", detail}
	}
	return result{name, "ok", "nothing queued beyond " + limit.Round(time.Minute).String()}
}

// newestSuccess is when a run in the list last finished successfully, or the
// zero time if none has.
func newestSuccess(runs []run) time.Time {
	var newest time.Time
	for _, r := range runs {
		if r.Status == "completed" && r.Conclusion == "success" && r.UpdatedAt.After(newest) {
			newest = r.UpdatedAt
		}
	}
	return newest
}

// A scheduled workflow that stops running is invisible: there is no failed run
// to notice, only an absence. This looks for the absence.
//
// It asks about ONE workflow, the drift check, and that is the whole point. It
// used to ask about every scheduled run in the repository, and this patrol is
// itself a scheduled workflow - so the moment the patrol went green, its own
// successes would have satisfied the one check that exists to notice the
// nightly had stopped. It was only right before because the patrol was failing
// too.
func (c *client) scheduledTierIsActuallyRunning(workflow string, within time.Duration) result {
	const name = "scheduled tier still completing"
	runs, err := c.runs(workflow, "event=schedule")
	if err != nil {
		return result{name, "unknown", "could not ask GitHub: " + err.Error()}
	}
	newest := newestSuccess(runs)
	if newest.IsZero() {
		return result{name, "fail", "no scheduled run has ever succeeded, so nothing is confirming drift is being checked"}
	}
	if age := time.Since(newest); age > within {
		return result{name, "fail", fmt.Sprintf(
			"last successful scheduled run was %s ago, over the %s limit.\n"+
				"       The drift check is the only thing that notices a machine changed by hand.",
			age.Round(time.Hour), within)}
	}
	return result{name, "ok", "last succeeded " + time.Since(newest).Round(time.Hour).String() + " ago"}
}

// A converge that failed left the estate part-way to a state somebody merged.
func (c *client) lastConvergeDidNotFail() result {
	const name = "last converge did not fail"
	runs, err := c.runs("", "branch=main&event=push")
	if err != nil {
		return result{name, "unknown", "could not ask GitHub: " + err.Error()}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt.After(runs[j].CreatedAt) })
	for _, r := range runs {
		if !strings.Contains(strings.ToLower(r.Name), "deploy infrastructure") || r.Status != "completed" {
			continue
		}
		if r.Conclusion == "success" {
			return result{name, "ok", "succeeded " + time.Since(r.UpdatedAt).Round(time.Hour).String() + " ago"}
		}
		return result{name, "fail", fmt.Sprintf(
			"the most recent completed converge on main ended %q.\n"+
				"       The estate may be part-way to a state that was already merged.", r.Conclusion)}
	}
	return result{name, "skip", "no completed converge on main yet"}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "security patrol: "+msg)
	os.Exit(2)
}
