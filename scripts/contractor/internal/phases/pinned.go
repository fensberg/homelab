package phases

import (
	"fmt"

	"homelab/contractor/internal/run"
	"homelab/contractor/pin"
)

// placeModules puts the repository as it was at the site's pinned commit
// where the site's roots read their modules from (management/pins.json; see
// homelab/contractor/pin). Every verb that runs tofu against an estate does
// this before its first tofu command, so a site runs the version it is
// pinned to and never the working tree.
func placeModules(ctx *run.Context) error {
	sha, err := pin.Place(ctx.RepoRoot, ctx.Site, pin.Exec)
	if err != nil {
		return fmt.Errorf("placing the modules %s is pinned to: %w", ctx.Site, err)
	}
	run.Ok("modules in place at the site's pin, " + sha[:7])
	return nil
}
