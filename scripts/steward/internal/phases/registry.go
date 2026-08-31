package phases

import (
	"time"

	"homelab/steward/internal/run"
)

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
// PlanPhases answers "what would a converge do" and changes nothing.
//
// It attaches to the estate's state like a converge does, because a plan
// against no state is a plan to build everything - and then stops. No overlay,
// because minting a tailnet key is a side effect and this sequence has none.
var PlanPhases = []string{
	"render", "verify", "attach", "plan", "sterilize",
}

var ConvergePhases = []string{
	"render", "overlay", "verify", "attach",
	"compute", "cluster", "health", "backup", "sterilize",
}

// Sequences is every sequence there is.
//
// Declared once so the contract test cannot go stale: a fourth sequence added
// without touching this list would be invisible to the check that every
// dispatched phase belongs to one, which is the check that stops a phase
// existing that nothing can ever run.
var Sequences = [][]string{AllPhases, ConvergePhases, PlanPhases}

// Run dispatches a single phase by name and reports how long it took.
//
// Timed here rather than in each phase: one place, and no phase can forget.
func Run(ctx *run.Context, name string) error {
	start := time.Now()
	err := dispatch(ctx, name)
	run.Elapsed(time.Since(start))
	return err
}

func dispatch(ctx *run.Context, name string) error {
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
	case "plan":
		return Plan(ctx)
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
