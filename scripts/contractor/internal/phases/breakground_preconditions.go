package phases

import (
	"encoding/json"
	"fmt"
	"strings"

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

Authenticate with 'gh auth login' and re-run, or check by hand with
'gh run list --workflow %s' and cancel anything pending for %s.

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
func fetchActiveRuns() ([]activeRun, error) {
	out, err := run.CmdOutputQuiet(".", "gh", "run", "list",
		"--workflow", deployWorkflow,
		"--json", "number,databaseId,status,headBranch",
		"--limit", "20")
	if err != nil {
		return nil, err
	}
	var runs []activeRun
	if err := json.Unmarshal([]byte(out), &runs); err != nil {
		return nil, fmt.Errorf("reading the list of workflow runs: %w", err)
	}

	for i := range runs {
		if !unfinished[runs[i].Status] {
			continue
		}
		// Best-effort. A run whose jobs cannot be read keeps an empty list,
		// which pendingForSite treats as pending - the safe reading.
		jobs, err := run.CmdOutputQuiet(".", "gh", "run", "view",
			fmt.Sprintf("%d", runs[i].DatabaseID), "--json", "jobs", "--jq", ".jobs[].name")
		if err != nil {
			continue
		}
		for _, line := range strings.Split(jobs, "\n") {
			if l := strings.TrimSpace(line); l != "" {
				runs[i].Jobs = append(runs[i].Jobs, l)
			}
		}
	}
	return runs, nil
}
