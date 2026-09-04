package phases

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"homelab/contractor/internal/run"
)

// Ignition surveys the ground before building on it.
//
// A queued action is a landmine. The deploy workflow's runner is a pod inside
// the cluster it converges, so a torn-down estate leaves converges queued with
// nothing to pick them up, and GitHub holds them for roughly a day. They are
// harmless only while no runner exists - and ignition is exactly what ends
// that, because Flux brings the runner up near the end of the sequence. A job
// queued before the run started then acquires a runner partway through it and
// converges against a half-built estate, applying whatever commit it was
// queued at.
//
// Scoped to the site being built, and that scoping is the point. The converge
// job is a matrix over sites, so a run queued while site0 is being converged
// says nothing about whether site1 is safe to ignite. A guard that refuses to
// build a second site because the first one is busy is a guard somebody
// switches off, and then it is not protecting the first site either.

// BreakGroundPrecondition is something that must be true before ignition
// creates anything.
type BreakGroundPrecondition struct {
	Name  string
	Check func(site string) error
}

// BreakGroundPreconditions is what must hold before ignition begins.
func BreakGroundPreconditions() []BreakGroundPrecondition {
	return []BreakGroundPrecondition{
		{
			Name:  "no deploy is queued or running for this site",
			Check: noPendingDeploysForSite,
		},
	}
}

// CheckBreakGroundPreconditions runs them for one site.
func CheckBreakGroundPreconditions(site string) error {
	for _, p := range BreakGroundPreconditions() {
		run.Info("checking " + p.Name + " ...")
		if err := p.Check(site); err != nil {
			return err
		}
		run.Ok(p.Name)
	}
	return nil
}

// deployWorkflow is the file whose runs can collide with an ignition.
const deployWorkflow = "deploy-infrastructure.yml"

// activeRun is a workflow run that has not finished, and the jobs it holds.
type activeRun struct {
	Number     int      `json:"number"`
	DatabaseID int64    `json:"databaseId"`
	Status     string   `json:"status"`
	HeadBranch string   `json:"headBranch"`
	Jobs       []string `json:"-"`
}

// unfinished is every status in which a run still has work that could start.
// Listed rather than inferred from "not completed", so a status GitHub adds
// later is not silently treated as harmless.
var unfinished = map[string]bool{
	"queued": true, "in_progress": true, "waiting": true,
	"requested": true, "pending": true,
}

// convergeJobFor is the job name the deploy workflow renders for a site:
// `Converge ${{ matrix.site }}`.
func convergeJobFor(site string) string { return "Converge " + site }

// pendingForSite picks the runs that would converge this particular site.
//
// Separated from the fetching because this is the decision, and the decision is
// the part that must not blow up an unrelated site. A run whose jobs are
// unknown counts as pending: not being able to tell what a queued job will do
// is not the same as knowing it will do nothing, and the window being guarded
// is twenty minutes long with nobody watching the middle of it.
func pendingForSite(runs []activeRun, site string) []activeRun {
	want := convergeJobFor(site)
	var out []activeRun
	for _, r := range runs {
		if !unfinished[r.Status] {
			continue
		}
		if len(r.Jobs) == 0 {
			out = append(out, r)
			continue
		}
		for _, j := range r.Jobs {
			if strings.Contains(j, want) {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

func noPendingDeploysForSite(site string) error {
	runs, err := fetchActiveRuns()
	if err != nil {
		// Fail closed. "I could not ask" is not "nothing is pending".
		return fmt.Errorf(`could not ask GitHub whether a deploy is pending, so this ignition cannot show it is safe to start.

A queued converge acquires a runner the moment Flux brings one up, which
happens partway through this sequence - so it would apply against a half-built
estate. Refusing rather than assuming.

If this is a workflow job, it needs actions:read permission; a 403 above means exactly
that. Otherwise check by hand in the Actions tab for %s and cancel anything
pending for %s.

Underlying error: %w`, deployWorkflow, site, err)
	}

	pending := pendingForSite(runs, site)
	if len(pending) == 0 {
		return nil
	}

	var names []string
	for _, r := range pending {
		names = append(names, fmt.Sprintf("#%d (%s, %s) - gh run cancel %d",
			r.Number, r.HeadBranch, r.Status, r.DatabaseID))
	}
	return fmt.Errorf(`%d deploy run(s) would converge %s during this ignition.

    %s

The runner is a pod inside the cluster this run is about to build, so those
jobs are waiting for it. Flux brings it up near the end of the sequence - they
would acquire a runner partway through and converge against a half-built
estate, applying whatever commit they were queued at.

Runs that only touch other sites are ignored, so everything listed above
genuinely targets this one`, len(pending), site, strings.Join(names, "\n    "))
}

// fetchActiveRuns lists unfinished deploy runs and the jobs each one holds.
//
// Asked over HTTP rather than by shelling out to `gh`.
//
// The first integration-tier run in this repository's history halted here with
// `exec: "gh": executable file not found in $PATH` - the runner image does not
// carry it, and the hand-written list in the guard that promises every binary
// did not have it either (#218). Installing gh would not have helped: that job
// holds only contents:read, and listing runs needs actions:read, so it would
// have been refused one step later.
//
// So the dependency comes out rather than the gap being filled. This is one
// GET against an API a zero-dependency Go program can already reach, and it
// works on a workstation with no gh installed at all.
func fetchActiveRuns() ([]activeRun, error) {
	slug, err := repoSlug()
	if err != nil {
		return nil, err
	}

	var listing struct {
		Runs []struct {
			ID         int64  `json:"id"`
			Number     int    `json:"run_number"`
			Status     string `json:"status"`
			HeadBranch string `json:"head_branch"`
		} `json:"workflow_runs"`
	}
	if err := getJSON(fmt.Sprintf("%s/repos/%s/actions/workflows/%s/runs?per_page=20",
		githubAPI, slug, deployWorkflow), &listing); err != nil {
		return nil, err
	}

	var runs []activeRun
	for _, r := range listing.Runs {
		if !unfinished[r.Status] {
			continue
		}
		a := activeRun{Number: r.Number, DatabaseID: r.ID, Status: r.Status, HeadBranch: r.HeadBranch}

		// Best-effort. A run whose jobs cannot be read keeps an empty list,
		// which pendingForSite treats as pending - the safe reading.
		var jobs struct {
			Jobs []struct {
				Name string `json:"name"`
			} `json:"jobs"`
		}
		if err := getJSON(fmt.Sprintf("%s/repos/%s/actions/runs/%d/jobs", githubAPI, slug, r.ID), &jobs); err == nil {
			for _, j := range jobs.Jobs {
				a.Jobs = append(a.Jobs, j.Name)
			}
		}
		runs = append(runs, a)
	}
	return runs, nil
}

// githubAPI is a variable so a test can point it somewhere hermetic. Nothing
// else reassigns it.
var githubAPI = "https://api.github.com"

// getJSON performs one read against the GitHub API.
//
// The token is optional: this repository is public and an unauthenticated read
// of its workflow runs succeeds. GITHUB_TOKEN is used when present, because a
// request carrying one is rate-limited far less and because the same code has
// to keep working if the repository is ever private.
func getJSON(url string, into any) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// The status is the diagnosis. A 403 here means the caller lacks
		// actions:read, which is the same refusal gh would have produced.
		return fmt.Errorf("GitHub answered %d for %s", resp.StatusCode, url)
	}
	return json.Unmarshal(body, into)
}

// repoSlug is owner/name for the repository this run belongs to.
//
// GITHUB_REPOSITORY in Actions, the origin remote on a workstation. git is
// present wherever this program runs, which is exactly what was not true of gh.
func repoSlug() (string, error) {
	if s := strings.TrimSpace(os.Getenv("GITHUB_REPOSITORY")); s != "" {
		return s, nil
	}
	out, err := run.CmdOutputQuiet(".", "git", "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("no GITHUB_REPOSITORY set and the origin remote could not be read: %w", err)
	}
	u := strings.TrimSuffix(strings.TrimSpace(out), ".git")
	if i := strings.Index(u, "github.com"); i >= 0 {
		u = strings.TrimLeft(u[i+len("github.com"):], ":/")
	}
	if strings.Count(u, "/") != 1 || u == "" {
		return "", fmt.Errorf("could not read owner/name out of the origin remote")
	}
	return u, nil
}
