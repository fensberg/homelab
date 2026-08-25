package phases

import "homelab/ignite/internal/run"

// AllPhases is the full ignition sequence, in order.
var AllPhases = []string{
	"render", "overlay", "hypervisor", "verify",
	"compute", "cluster", "migrate", "backup", "sterilize",
}

// Run dispatches a single phase by name.
func Run(ctx *run.Context, name string) error {
	switch name {
	case "render":
		return Render(ctx)
	case "overlay":
		return Overlay(ctx)
	case "hypervisor":
		return Hypervisor(ctx)
	case "verify":
		return Verify(ctx)
	case "compute":
		return Compute(ctx)
	case "cluster":
		return Cluster(ctx)
	case "migrate":
		return Migrate(ctx)
	case "backup":
		return Backup(ctx)
	case "sterilize":
		return Sterilize(ctx, false)
	default:
		panic("unknown phase: " + name)
	}
}
