package phases

import (
	"fmt"
	"os"
	"path/filepath"

	"homelab/contractor/config"
	"homelab/contractor/internal/run"
	"homelab/contractor/steps"
	"homelab/details/asbuilt"
)

// PlanAsBuilt plans the change in this workspace against a saved as-built
// record, with no credential and no network beyond fetching providers
// (#554).
//
// It is the plan a pull request gets. The record holds nothing real, so this
// can run anywhere - a hosted runner, a laptop - and what it prints is the
// same addresses-and-verbs summary `plan` prints. What it cannot see is drift
// since the record was taken; the nightly's real plan is what reports that.
//
// Not a phase: it renders nothing, reads no vault and attaches to no state,
// so none of the sequence around a phase applies to it.
func PlanAsBuilt(ctx *run.Context, recordDir string) error {
	run.WritePhase("Plan", "Show what this change would do, planned against the as-built record.")
	defer func() { _ = run.RemoveTreeIfExists(ctx.AsBuiltDir) }()
	// Planned with the modules the change would leave the site pinned to:
	// that is what merging it does to the site. A change to a module alone
	// moves no site, and plans as no change until a pin does.
	if err := placeModules(ctx); err != nil {
		return err
	}
	return planAsBuilt(ctx, recordDir, asbuilt.Exec)
}

func planAsBuilt(ctx *run.Context, recordDir string, tofu asbuilt.Tofu) error {
	if recordDir == "" {
		return fmt.Errorf("plan-as-built needs -record: the directory a record was saved to")
	}
	tpl, err := os.ReadFile(ctx.ConfigTpl)
	if err != nil {
		return err
	}
	// Each root against its own record, reported as one plan. The platform
	// root is configured from the cluster root's outputs, and plans here
	// with the stand-ins the cluster's record holds for them.
	var plans [][]byte
	for _, root := range ctx.Roots() {
		inputs := asbuilt.PlanInputs{
			Root: root.Dir, Work: ctx.AsBuiltDir, Record: filepath.Join(recordDir, root.Name),
			Template: tpl, Site: ctx.Site,
			Sequence: steps.Plan(root.Name),
		}
		if root.Name == config.PlatformRoot {
			state, _, _, err := asbuilt.Read(filepath.Join(recordDir, config.ClusterRoot))
			if err != nil {
				return err
			}
			if inputs.Vars, err = asbuilt.OutputVars(state, platformInputs...); err != nil {
				return err
			}
		}
		plan, _, err := asbuilt.PlanAgainst(inputs, tofu)
		// The copy is of one root, in a directory the next root's takes.
		if rmErr := run.RemoveTreeIfExists(ctx.AsBuiltDir); rmErr != nil && err == nil {
			err = rmErr
		}
		if err != nil {
			return fmt.Errorf("the %s root: %w", root.Name, err)
		}
		plans = append(plans, plan)
	}
	raw, err := asbuilt.MergePlans(plans...)
	if err != nil {
		return err
	}
	summary, err := summarisePlan(raw)
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Println(summary)
	if ctx.CommentOut != "" {
		body := commentBody(ctx.Site, summary, plannedCommit())
		if err := os.WriteFile(ctx.CommentOut, []byte(body), 0o644); err != nil {
			return fmt.Errorf("writing the comment body: %w", err)
		}
	}
	return nil
}
