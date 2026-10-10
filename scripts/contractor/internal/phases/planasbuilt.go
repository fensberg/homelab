package phases

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"homelab/contractor/config"
	"homelab/contractor/internal/run"
	"homelab/contractor/steps"
	"homelab/details/asbuilt"
	"homelab/details/platform"
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
	defer func() {
		_ = run.RemoveIfExists(ctx.RegistryCredential)
		_ = forgetCredential(ctx)
	}()
	// Planned first with the release the change would leave the site on,
	// which the verb named before this: that is what merging it does to the
	// site. A change to a module alone moves no site, so it is then planned
	// again with the modules as the change has them (planAsBuilt).
	if err := fetchCredential(ctx); err != nil {
		return err
	}
	return planAsBuilt(ctx, recordDir, asbuilt.Exec, placeUnreleased)
}

// placeUnreleased puts this checkout's own copy of what a release would hold
// where a root can be told to read it, and says where, as a root two
// directories down from the top of the repository names it.
func placeUnreleased(repoRoot string) (string, error) {
	// proved by TestEveryProgramRunIsNamedByAConstantOrIsTheOperators
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	tracked, err := exec.Command("git", "-C", repoRoot, "ls-files", "-z").Output()
	if err != nil {
		return "", fmt.Errorf("listing what this checkout tracks: %w", err)
	}
	if _, err := platform.PlaceUnreleased(repoRoot, strings.Split(strings.TrimRight(string(tracked), "\x00"), "\x00")); err != nil {
		return "", err
	}
	return "../../" + platform.Unreleased, nil
}

// planAsBuilt plans twice, and the second is what makes the first worth
// reading on a change to the modules.
//
// A site runs the modules of the release its line names, so a pull request
// that changes a module changes nothing a site runs: planned as the site
// runs today it says "no changes", truthfully, and the effect only appeared
// later, on the one-line change that moves the site to the new release - the
// decision on one pull request and its evidence on another. So the same
// plan is made again with the modules as they are in this checkout, and the
// reader is shown both: what merging does now, and what the site would
// change once it runs this.
func planAsBuilt(ctx *run.Context, recordDir string, tofu asbuilt.Tofu, unreleased func(repoRoot string) (string, error)) error {
	if recordDir == "" {
		return fmt.Errorf("plan-as-built needs -record: the directory a record was saved to")
	}
	tpl, err := os.ReadFile(ctx.ConfigTpl)
	if err != nil {
		return err
	}
	merging, err := planRoots(ctx, recordDir, tpl, tofu, "")
	if err != nil {
		return err
	}
	modules, err := unreleased(ctx.RepoRoot)
	defer func() { _ = os.RemoveAll(filepath.Join(ctx.RepoRoot, platform.Unreleased)) }()
	if err != nil {
		return fmt.Errorf("placing this checkout's modules to plan with them: %w", err)
	}
	adopted, err := planRoots(ctx, recordDir, tpl, tofu, modules)
	if err != nil {
		return fmt.Errorf("with the modules as they are in this change: %w", err)
	}

	summary := merging
	if adopted != merging {
		summary = fmt.Sprintf("%s\n\n%s\n\n%s", strings.TrimRight(merging, "\n"), onceAdopted(ctx.Site), adopted)
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

// onceAdopted is the line between the two plans. Said only when they differ:
// a change that touches no module plans the same both ways, and saying so
// twice would teach a reader to skip the second half.
func onceAdopted(site string) string {
	return fmt.Sprintf("Merging this does only the above: %s runs the modules of the release it is pinned to.\nOnce it runs the modules as they are in this change, the whole plan is:", site)
}

// planRoots plans each root against its own record and reports them as one
// plan. The platform root is configured from the cluster root's outputs, and
// plans here with the stand-ins the cluster's record holds for them. With
// modules named, the roots read those and not the site's release.
func planRoots(ctx *run.Context, recordDir string, tpl []byte, tofu asbuilt.Tofu, modules string) (string, error) {
	var plans [][]byte
	for _, root := range ctx.Roots() {
		inputs := asbuilt.PlanInputs{
			Root: root.Dir, Work: ctx.AsBuiltDir, Record: filepath.Join(recordDir, root.Name),
			Template: tpl, Site: ctx.Site,
			Sequence: steps.Plan(root.Name),
			Vars:     map[string]string{},
		}
		if root.Name == config.PlatformRoot {
			state, _, _, err := asbuilt.Read(filepath.Join(recordDir, config.ClusterRoot))
			if err != nil {
				return "", err
			}
			if inputs.Vars, err = asbuilt.OutputVars(state, platformInputs...); err != nil {
				return "", err
			}
		}
		// The hardware's facts, as the record holds them from the last
		// converge. A record taken before they were read has none, and the
		// root's own default stands for it.
		if root.Name == config.ClusterRoot {
			state, _, _, err := asbuilt.Read(filepath.Join(recordDir, root.Name))
			if err != nil {
				return "", err
			}
			if facts, err := asbuilt.OutputVars(state, hardwareInput); err == nil {
				inputs.Vars[hardwareInput] = facts[hardwareInput]
			}
		}
		if modules != "" {
			inputs.Vars[strings.TrimPrefix(platform.UnreleasedVariable, "TF_VAR_")] = modules
		}
		plan, _, err := asbuilt.PlanAgainst(inputs, tofu)
		if rmErr := run.RemoveTreeIfExists(ctx.AsBuiltDir); rmErr != nil && err == nil {
			err = rmErr
		}
		if err != nil {
			return "", fmt.Errorf("the %s root: %w", root.Name, err)
		}
		plans = append(plans, plan)
	}
	raw, err := asbuilt.MergePlans(plans...)
	if err != nil {
		return "", err
	}
	return summarisePlan(raw)
}
