package phases

import (
	"encoding/json"
	"fmt"
	"strings"

	"homelab/contractor/internal/run"
)

// Ignition refuses to start while a deploy is queued.
//
// A queued action is a landmine. The deploy workflow's runner is a pod inside
// the cluster it converges, so tearing an estate down leaves every converge
// queued with nothing to pick it up - and GitHub holds it for roughly a day.
// The job is harmless only while no runner exists.
//
// Ignition is what ends that. Flux brings the runner up near the end of the
// sequence, so a job queued before the run started acquires a runner partway
// through it and applies its own commit against a half-built estate. Two
// things writing one state, one of them a job nobody remembers queuing.
//
// So this is asked before the first phase rather than discovered afterwards.
// The window it protects is the whole of ignition, and it opens precisely when
// the run is at its most fragile.

// BreakGroundPrecondition is something that must be true before ignition
// creates anything.
type BreakGroundPrecondition struct {
	Name  string
	Check func() error
}

// BreakGroundPreconditions is what must hold before ignition begins.
func BreakGroundPreconditions() []BreakGroundPrecondition {
	return []BreakGroundPrecondition{
		{
			Name:  "no deploy is queued or running",
			Check: noPendingDeploys,
		},
	}
}

// CheckBreakGroundPreconditions runs them.
func CheckBreakGroundPreconditions() error {
	for _, p := range BreakGroundPreconditions() {
		run.Info("checking " + p.Name + " ...")
		if err := p.Check(); err != nil {
			return err
		}
		run.Ok(p.Name)
	}
	return nil
}

// deployWorkflow is the file whose runs can collide with an ignition.
const deployWorkflow = "deploy-infrastructure.yml"

type workflowRun struct {
	Number     int    `json:"number"`
	Status     string `json:"status"`
	HeadBranch string `json:"headBranch"`
}

// noPendingDeploys asks GitHub whether anything is waiting to converge.
func noPendingDeploys() error {
	out, err := run.CmdOutputQuiet(".", "gh", "run", "list",
		"--workflow", deployWorkflow,
		"--status", "queued",
		"--json", "number,status,headBranch",
		"--limit", "20")
	if err != nil {
		// Fail closed. "I could not ask" is not "nothing is pending", and the
		// thing being guarded against is a job firing unattended partway
		// through a run that takes twenty minutes.
		return fmt.Errorf(`could not ask GitHub whether a deploy is queued, so this ignition cannot show it is safe to start.

A queued converge acquires a runner the moment Flux brings one up, which
happens partway through this sequence - so it would apply against a
half-built estate. Refusing rather than assuming.

Authenticate with 'gh auth login' and re-run, or cancel any queued runs by
hand and confirm with 'gh run list --workflow %s'.

Underlying error: %w`, deployWorkflow, err)
	}

	var runs []workflowRun
	if err := json.Unmarshal([]byte(out), &runs); err != nil {
		return fmt.Errorf("could not read the list of workflow runs, so this ignition cannot show it is safe to start: %w", err)
	}
	if len(runs) == 0 {
		return nil
	}

	var names []string
	for _, r := range runs {
		names = append(names, fmt.Sprintf("#%d (%s, %s)", r.Number, r.HeadBranch, r.Status))
	}
	return fmt.Errorf(`%d deploy run(s) are queued and will fire during this ignition.

    %s

The runner is a pod inside the cluster this run is about to build. Those jobs
are waiting for it, and Flux brings it up near the end of the sequence - so
they would acquire a runner partway through and converge against a half-built
estate, applying whatever commit they were queued at.

Cancel them first:

    gh run cancel <id>

'gh run list --workflow %s' lists them with their ids`,
		len(runs), strings.Join(names, "\n    "), deployWorkflow)
}
