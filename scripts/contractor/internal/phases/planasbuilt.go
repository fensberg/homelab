package phases

import (
	"fmt"
	"os"

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
	raw, _, err := asbuilt.PlanAgainst(asbuilt.PlanInputs{
		Root: ctx.ClusterDir, Work: ctx.AsBuiltDir, Record: recordDir,
		Template: tpl, Site: ctx.Site,
		Sequence: steps.Plan(),
	}, tofu)
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
