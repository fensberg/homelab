package phases

import (
	"fmt"
	"os"
	"strings"

	"homelab/contractor/internal/run"
)

// A converge that is not the tip of main refuses to run.
//
// The deploy workflow groups concurrency per ref with cancel-in-progress
// false, which is right for an apply - nothing should cancel one mid-flight.
// The consequence is that a run queued for a runner which does not exist sits
// there, holding the slot, until GitHub gives up roughly a day later.
//
// The danger is not the waiting. It is what happens if a runner appears while
// the job is still queued: it converges the estate to the commit it was
// queued at, which by then may be hours or days behind main, with nobody
// having asked for it. That has happened here - five refs blocked at once, the
// oldest pinned to a commit from two days earlier - and it happened again the
// moment the cluster was torn down, because the runner lives inside the
// cluster it converges.
//
// The workflow cannot fix this on its own: timeout-minutes only counts once a
// job is running. So the guard belongs here, in the thing that would do the
// applying, and it asks one question at the last possible moment - am I still
// current?

// ConvergePrecondition is something that must be true before a converge
// applies anything.
//
// Declared as data for the reason the teardown's are: a deleted call in a run
// of statements is a few green lines, while a deleted entry is a list that no
// longer matches what the test says a converge checks.
type ConvergePrecondition struct {
	Name  string
	Check func() error
}

// ConvergePreconditions is what must hold before a converge begins.
func ConvergePreconditions() []ConvergePrecondition {
	return []ConvergePrecondition{
		{
			Name:  "this commit is still the tip of main",
			Check: checkoutIsCurrent,
		},
	}
}

// CheckConvergePreconditions runs them, and is a no-op outside CI.
//
// Locally a converge from an older commit is a deliberate act by somebody who
// is present and watching. The failure this guards against is specifically the
// unattended one: a job queued hours ago, firing when a runner returns, with
// no human in the loop at all.
func CheckConvergePreconditions() error {
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		return nil
	}
	for _, p := range ConvergePreconditions() {
		run.Info("checking " + p.Name + " ...")
		if err := p.Check(); err != nil {
			return err
		}
		run.Ok(p.Name)
	}
	return nil
}

// checkoutIsCurrent compares this checkout against the published tip of main.
func checkoutIsCurrent() error {
	head, err := run.CmdOutputQuiet(".", "git", "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("could not read this checkout's commit, so it cannot be shown to be current: %w", err)
	}
	remote, err := run.CmdOutputQuiet(".", "git", "ls-remote", "origin", "refs/heads/main")
	if err != nil {
		// Fail closed. Being unable to establish that this converge is current
		// is not the same as it being current, and the whole point of this
		// check is the case where nobody is watching.
		return fmt.Errorf(`could not read the published tip of main, so this converge cannot show it is current.

Refusing rather than assuming: this check exists for runs nobody is watching,
and "I could not tell" must not behave like "yes". Underlying error: %w`, err)
	}

	tip := strings.Fields(remote)
	if len(tip) == 0 {
		return fmt.Errorf("origin has no main branch, so there is nothing to compare this converge against")
	}
	if strings.TrimSpace(head) == tip[0] {
		return nil
	}

	return fmt.Errorf(`this converge is not running the current tip of main.

Its checkout is %s and main is now %s.

Almost certainly this job was queued while no runner existed - the runner is a
pod inside the cluster it converges, so tearing the estate down leaves every
converge queued - and a runner has since appeared. Applying now would converge
the estate to a commit that has been superseded, which nobody asked for.

Cancel this run. Whatever is on main will converge on its own next merge, or
can be re-run deliberately`, short(head), short(tip[0]))
}

func short(sha string) string {
	s := strings.TrimSpace(sha)
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
