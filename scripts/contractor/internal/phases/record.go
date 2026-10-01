package phases

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"homelab/contractor/config"
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
// It reports what it found as names and counts, and saves the record to
// ctx.RecordOut when that is set and the record is publishable. Everything
// else it wrote is removed.
//
// IN A CONVERGE IT CANNOT FAIL THE RUN. By the time it runs the apply has
// succeeded, and a failure here would read to the aftermath as a failed
// converge and start a revert of a change that landed. So in a converge it
// warns and saves nothing; plans keep reading the previous record, which says
// when it was taken, and the nightly takes a fresh one.
//
// The report is safe to paste anywhere: resource types, attribute names and
// counts, the vocabulary summarisePlan already prints to a public log. No
// value, and no instance key, since a key can be a vault value.
func Record(ctx *run.Context) error {
	run.WritePhase("Record", "Take the as-built record: the estate as converged, with nothing real in it.")
	defer func() { _ = run.RemoveTreeIfExists(ctx.AsBuiltDir) }()
	return takeRecord(ctx, asbuilt.Exec)
}

// takeRecord is Record with its tofu handed to it, so the converge's rule -
// warn, never fail - is testable.
func takeRecord(ctx *run.Context, tofu asbuilt.Tofu) error {
	err := record(ctx, tofu)
	if err != nil && ctx.Converge {
		run.Warn("no as-built record was taken, and the converge stands: " + err.Error())
		run.Warn("plans read the previous record until the next one is taken")
		return nil
	}
	return err
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

	// Each root on its own, the cluster's first: the platform root is
	// configured from the cluster root's outputs, so its offline plan runs
	// with the stand-ins the cluster's record holds for them.
	meta := asbuilt.Meta{Site: ctx.Site, Commit: recordedCommit(), Taken: time.Now().UTC()}
	results := map[string]*asbuilt.Result{}
	for _, root := range ctx.Roots() {
		in, err := rootFor(ctx, root.Name, tofu)
		if err != nil {
			return err
		}
		inputs := asbuilt.Inputs{
			Root: in.Dir, Work: ctx.AsBuiltDir,
			Template: tpl, Rendered: rendered, Site: ctx.Site,
			MachineSecrets: root.Name == config.ClusterRoot,
			Progress:       run.Info,
		}
		if root.Name == config.PlatformRoot {
			if inputs.Vars, err = asbuilt.OutputVars(results[config.ClusterRoot].State, platformInputs...); err != nil {
				return err
			}
		}
		run.Info("recording the " + root.Name + " root")
		res, err := asbuilt.Take(inputs, tofu)
		// The workspace holds one root's offline copy, in a directory the
		// next root's takes.
		if rmErr := run.RemoveTreeIfExists(ctx.AsBuiltDir); rmErr != nil && err == nil {
			err = rmErr
		}
		var pending *asbuilt.NotConvergedError
		if errors.As(err, &pending) {
			summary, _ := summarisePlan(pending.Plan)
			return fmt.Errorf("%w:\n\n%s\nConverge first. A record taken now would describe these changes as built", err, summary)
		}
		if err != nil {
			return fmt.Errorf("the %s root: %w", root.Name, err)
		}
		run.Ok("the " + root.Name + " root matches its config")
		run.Ok("replaced " + describeSources(res.Replaced))
		if err := report(res); err != nil {
			return fmt.Errorf("the %s root: %w", root.Name, err)
		}
		results[root.Name] = res
	}
	if ctx.RecordOut == "" {
		return nil
	}
	// Written only once every root is publishable: half a record would plan
	// half a site and say nothing about the rest.
	for _, root := range ctx.Roots() {
		if err := asbuilt.Write(filepath.Join(ctx.RecordOut, root.Name), results[root.Name], meta); err != nil {
			return err
		}
	}
	run.Ok("the record is saved to " + ctx.RecordOut)
	return nil
}

// recordedCommit is the commit of main the estate was converged to, as a
// reader would recognise it.
func recordedCommit() string {
	if sha := os.Getenv("GITHUB_SHA"); len(sha) >= 7 {
		return sha[:7]
	}
	if head, err := run.CmdOutputQuiet(".", "git", "rev-parse", "--short", "HEAD"); err == nil {
		return strings.TrimSpace(head)
	}
	return ""
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
	if len(res.KeyedByAValue) > 0 {
		run.Warn(fmt.Sprintf("%d resource(s) are keyed by a real value, so every plan, apply and log that names one prints it:", len(res.KeyedByAValue)))
		for _, k := range res.KeyedByAValue {
			fmt.Println("    " + k)
		}
		problems = append(problems, "a resource address holds a real value; key the resource by a key from the config instead")
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
