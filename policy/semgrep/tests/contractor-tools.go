package phases

import (
	"context"
	"os/exec"
)

func queued(ctx context.Context, run func(string, ...string) error) {
	// ruleid: the-contractor-does-not-shell-out-to-gh
	_ = exec.Command("gh", "run", "list")
	// ruleid: the-contractor-does-not-shell-out-to-gh
	_ = exec.CommandContext(ctx, "gh", "api", "repos/o/r/actions/runs")
	// ruleid: the-contractor-does-not-shell-out-to-gh
	_ = run("gh", "run", "list")
	// ok: the-contractor-does-not-shell-out-to-gh
	_ = exec.Command("tofu", "plan")
	// ok: the-contractor-does-not-shell-out-to-gh
	_ = run("git", "log", "--format=gh")
}
