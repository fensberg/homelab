package phases

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"homelab/contractor/internal/run"
)

// The safety officer is asked before a site is destroyed, and a teardown it
// does not clear does not start.
//
// It is another program (scripts/safety-officer), and that is the point: this
// one holds the detonator, so the party that says whether the explosion is
// safe is not a step of it that a flag or a later edit here could step
// around. It is given the site's name and nothing else. It reads what the
// site holds that should outlive it, looks at each copy itself with a key
// that cannot change one, and answers by how it exits.
//
// Run from source rather than from a binary built earlier, as the lawyer is:
// what is asked is then what this checkout declares, and never what some
// older build knew to ask.
func clearedBySafetyOfficer(ctx *run.Context) error {
	run.Info("asking the safety officer whether this loses anything that should outlive the site")
	cmd := exec.Command("go", "run", "-C", filepath.Join(ctx.RepoRoot, "scripts", "safety-officer"), ".",
		"clear", "-site", ctx.Site, "-destroying", "site")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf(`the safety officer did not clear this teardown (%w).

Nothing has been destroyed. What it found is above. There is no flag that
skips it: make the copy it asks for, or stop the site holding what it names,
and run the teardown again`, err)
	}
	return nil
}
