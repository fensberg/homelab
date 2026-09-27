package phases

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"homelab/contractor/internal/run"
	"homelab/details/asbuilt"
)

// Record takes the site's as-built record: its state as last converged, with
// nothing real left in it, proven quiet against an offline plan (#554). The
// work is details/asbuilt's; this is the phase around it.
//
// It is the half of the design that holds a credential. A pull request's plan
// will run against the record on a hosted runner and hold none, so the record
// is made by a run that already decrypts the state to do its job, and what it
// produces is worthless by construction.
//
// FOR NOW IT PUBLISHES NOTHING. It reports what it found as names and counts
// and removes everything it wrote. The first question is whether the record
// comes out quiet against the real estate, which a fabricated state could not
// settle.
//
// The report is safe to paste anywhere: resource types, attribute names and
// counts, the vocabulary summarisePlan already prints to a public log. No
// value, and no instance key, since a key can be a vault value.
func Record(ctx *run.Context) error {
	run.WritePhase("Record", "Take the as-built record: the estate as converged, with nothing real in it.")
	defer func() { _ = run.RemoveTreeIfExists(ctx.AsBuiltDir) }()
	return record(ctx, execTofu)
}

func record(ctx *run.Context, tofu asbuilt.Tofu) error {
	tpl, err := os.ReadFile(ctx.ConfigTpl)
	if err != nil {
		return err
	}
	rendered, err := os.ReadFile(ctx.ConfigRendered)
	if err != nil {
		return fmt.Errorf("the rendered config is missing, so Render did not run: %w", err)
	}
	defer run.Wipe(rendered)

	res, err := asbuilt.Take(asbuilt.Inputs{
		Root: ctx.ClusterDir, Work: ctx.AsBuiltDir,
		Template: tpl, Rendered: rendered, Site: ctx.Site,
		Progress: run.Info,
	}, tofu)
	var pending *asbuilt.NotConvergedError
	if errors.As(err, &pending) {
		summary, _ := summarisePlan(pending.Plan)
		return fmt.Errorf("%w:\n\n%s\nConverge first. A record taken now would describe these changes as built", err, summary)
	}
	if err != nil {
		return err
	}
	run.Ok("the estate matches its config")
	run.Ok("replaced " + describeSources(res.Replaced))
	return report(res)
}

// report prints what the record came to, and fails the run unless the record
// is fit to publish.
func report(res *asbuilt.Result) error {
	fmt.Println()
	var problems []string
	if res.Quiet {
		run.Ok(fmt.Sprintf("the offline plan is quiet after %d round(s)", res.Rounds))
	} else {
		summary, _ := summarisePlan(res.LastPlan)
		run.Warn(fmt.Sprintf("the offline plan is still not quiet after %d rounds. What remains:", res.Rounds))
		fmt.Println(summary)
		problems = append(problems, "the record is not quiet, so every plan against it would show these")
	}
	if len(res.Before) == 0 {
		run.Ok("no real value survived the replacing")
	} else {
		run.Warn(fmt.Sprintf("%d place(s) still held a real value after replacing:", len(res.Before)))
		for _, f := range res.Before {
			fmt.Println("    " + f.String())
		}
		problems = append(problems, "real values survived the replacing")
	}
	if computed := res.Computed(); len(computed) > 0 {
		run.Info(fmt.Sprintf("%d value(s) marked sensitive came back, computed by the offline plan from the record and public code:", len(computed)))
		for _, f := range computed {
			fmt.Println("    " + f.String())
		}
	}
	if !res.Publishable() {
		return fmt.Errorf("this record could not be published: %s", strings.Join(problems, "; "))
	}
	run.Ok("the record is quiet and holds nothing real")
	return nil
}

// describeSources says how many real values were replaced and where they
// came from, forms included.
func describeSources(counts map[string]int) string {
	total := 0
	names := make([]string, 0, len(counts))
	for k, n := range counts {
		total += n
		names = append(names, k)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, k := range names {
		parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
	}
	return fmt.Sprintf("%d real values (%s)", total, strings.Join(parts, ", "))
}

func execTofu(dir string, env []string, args ...string) ([]byte, []byte, error) {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	c := exec.Command("tofu", args...)
	c.Dir = dir
	if env != nil {
		c.Env = env
	}
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	err := c.Run()
	return out.Bytes(), errb.Bytes(), err
}
