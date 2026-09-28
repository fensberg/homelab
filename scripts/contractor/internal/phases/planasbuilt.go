package phases

import (
	"fmt"
	"os"
	"strings"

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
	raw, meta, err := asbuilt.PlanAgainst(asbuilt.PlanInputs{
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
		body := againstRecord(commentBody(ctx.Site, summary, plannedCommit()), meta)
		if err := os.WriteFile(ctx.CommentOut, []byte(body), 0o644); err != nil {
			return fmt.Errorf("writing the comment body: %w", err)
		}
	}
	return nil
}

// againstRecord says, under the heading, what the plan was compared with. A
// record is only as current as the last converge or nightly that took it, so
// the reader is told which commit and when rather than left to assume the
// estate as of this morning.
func againstRecord(body string, meta asbuilt.Meta) string {
	head, rest, _ := strings.Cut(body, "\n## ")
	title, after, _ := strings.Cut(rest, "\n")
	note := fmt.Sprintf("Compared with the estate as recorded after `%s` converged, on %s. Drift since then is the nightly's to report.",
		meta.Commit, meta.Taken.Format("2006-01-02 15:04 UTC"))
	return head + "\n## " + title + "\n\n" + note + "\n" + after
}
