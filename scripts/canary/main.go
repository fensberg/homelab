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

// A check is one question with one answer. Each returns a short status line;
// an error means the estate is unhealthy, not that the check misfired - a
// check that cannot run says so by returning a skip.
type result struct {
	name   string
	status string // "ok", "fail", "skip"
	detail string
}

func main() {
	var (
		repo         = flag.String("repo", envOr("GITHUB_REPOSITORY", ""), "owner/name to inspect")
		queuedFor    = flag.Duration("max-queued", 30*time.Minute, "how long a run may sit queued before that is a fault")
		nightlyEvery = flag.Duration("nightly-within", 30*time.Hour, "the scheduled tier must have finished within this")
	)
	flag.Parse()

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
		c.scheduledTierIsActuallyRunning(*nightlyEvery),
		c.lastConvergeDidNotFail(),
	}

	var failed int
	fmt.Println("estate canary")
	fmt.Println(strings.Repeat("-", 60))
	for _, r := range results {
		mark := map[string]string{"ok": "[ok]  ", "fail": "[FAIL]", "skip": "[skip]"}[r.status]
		fmt.Printf("%s %-34s %s\n", mark, r.name, r.detail)
		if r.status == "fail" {
			failed++
		}
	}
	fmt.Println(strings.Repeat("-", 60))

	if failed > 0 {
		fmt.Printf("\n%d check(s) failed. The estate is not answering for itself.\n", failed)
		os.Exit(1)
	}
	fmt.Println("\nthe estate is answering for itself")
}

type client struct {
	repo  string
	token string
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

func (c *client) runs(query string) ([]run, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/actions/runs?per_page=100&%s", c.repo, query)
	req, err := http.NewRequest(http.MethodGet, url, nil)
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

// The check that would have caught the runner deprecation, the dead listener
// before it, and the next cause nobody has met yet. It asks about the symptom
// rather than any one mechanism: work is not being picked up.
func (c *client) noRunStuckInTheQueue(limit time.Duration) result {
	const name = "no run stuck in the queue"
	stuck := map[string]bool{"queued": true, "pending": true, "waiting": true}

	var worst time.Duration
	var count int
	for _, status := range []string{"queued", "pending", "waiting"} {
		runs, err := c.runs("status=" + status)
		if err != nil {
			return result{name, "skip", "could not ask GitHub: " + err.Error()}
		}
		for _, r := range runs {
			if !stuck[r.Status] {
				continue
			}
			if age := time.Since(r.CreatedAt); age > limit {
				count++
				if age > worst {
					worst = age
				}
			}
		}
	}
	if count > 0 {
		return result{name, "fail", fmt.Sprintf(
			"%d run(s) queued longer than %s, oldest %s.\n"+
				"       Work is not being picked up. A job that never starts never fails:\n"+
				"       timeout-minutes only counts once it is running, so nothing else reports this.",
			count, limit.Round(time.Minute), worst.Round(time.Minute))}
	}
	return result{name, "ok", "nothing queued beyond " + limit.Round(time.Minute).String()}
}

// A scheduled workflow that stops running is invisible: there is no failed run
// to notice, only an absence. This looks for the absence.
func (c *client) scheduledTierIsActuallyRunning(within time.Duration) result {
	const name = "scheduled tier still completing"
	runs, err := c.runs("event=schedule")
	if err != nil {
		return result{name, "skip", "could not ask GitHub: " + err.Error()}
	}
	var newest time.Time
	for _, r := range runs {
		if r.Status == "completed" && r.Conclusion == "success" && r.UpdatedAt.After(newest) {
			newest = r.UpdatedAt
		}
	}
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
	runs, err := c.runs("branch=main&event=push")
	if err != nil {
		return result{name, "skip", "could not ask GitHub: " + err.Error()}
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
	fmt.Fprintln(os.Stderr, "canary: "+msg)
	os.Exit(2)
}
