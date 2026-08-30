package phases

import "homelab/steward/internal/run"

// AllPhases is the full ignition sequence, in order.
var AllPhases = []string{
	"render", "overlay", "hypervisor", "verify",
	"compute", "cluster", "health", "migrate", "backup", "sterilize",
}

// ConvergePhases applies a change to an estate that already exists.
//
// Two differences from AllPhases, and both are the point. It begins with
// attach, which connects to the state already in the cluster instead of
// starting from an empty workspace. And it has no migrate: state is already in
// Postgres, and migrate's -force-copy would overwrite it with whatever this
// workspace happened to hold.
//
// hypervisor is absent because it is slow, reboots-adjacent and unnecessary
// for a change that does not add a hypervisor; run it on its own with
// -phase hypervisor when one is added.
var ConvergePhases = []string{
	"render", "overlay", "verify", "attach",
	"compute", "cluster", "health", "backup", "sterilize",
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
	case "attach":
		return Attach(ctx)
	case "compute":
		return Compute(ctx)
	case "cluster":
		return Cluster(ctx)
	case "health":
		return Health(ctx)
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
