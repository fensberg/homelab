package phases

import (
	"fmt"
	"os"

	"homelab/contractor/config"
	"homelab/contractor/internal/run"
	"homelab/contractor/steps"
	"homelab/details/asbuilt"
)

// Cluster applies the Talos config, bootstraps etcd, and installs Flux. What
// it applies, and in what order, is declared in homelab/contractor/steps.
func Cluster(ctx *run.Context) error {
	run.WritePhase("Cluster", "Apply Talos config, bootstrap etcd, install Flux.")

	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		return err
	}
	net, err := config.ResolveSiteNetwork(cfg, ctx.Site)
	if err != nil {
		return err
	}
	if err := applySteps(ctx, "cluster", siteHas{workers: len(net.WorkerIPs) > 0, zones: len(net.DMZIPs) > 0}); err != nil {
		return err
	}
	run.Ok("cluster is up and Flux is reconciling")
	return nil
}

// siteHas is what a site has that some steps are only for.
type siteHas struct{ workers, zones bool }

// applySteps applies one phase's declared steps, in order, each in the root
// it names. A step for workers is skipped when the site has none, and one
// for untrusted zones when it has none of those.
func applySteps(ctx *run.Context, phase string, has siteHas) error {
	for _, st := range steps.Of(phase) {
		if (st.WhenWorkers && !has.workers) || (st.WhenZones && !has.zones) {
			continue
		}
		in, err := rootFor(ctx, st.Root, asbuilt.Exec)
		if err != nil {
			return err
		}
		// The step that finishes the cluster root asks the operating system
		// whether every node is Ready, and stops the converge if one is not.
		// A machine renamed by the steps before it has joined again under
		// its new name and left the node it was behind, which is never Ready
		// again. So what a rename left is cleared before that question, on a
		// converge: a build has renamed nothing. Cleared in the health gate
		// alone, it was cleared too late - the question came first, waited
		// ten minutes on a node that no longer had a machine, and failed
		// (#747).
		if ctx.TakenOverOK && st.Root == config.ClusterRoot && len(st.Targets) == 0 {
			if err := clearFormerNodes(ctx); err != nil {
				return err
			}
		}
		// A new untrusted worker joins closed to work only if the node it
		// joins as is new too, so what its predecessor left is removed
		// before it is configured. A build has no predecessors.
		//
		// And a new one is given its configuration whatever its plan says.
		// It was built in an earlier step, so by this one nothing shows it
		// changing, and the configuration that replacing it ought to send
		// again is planned as already sent: the machine waits unconfigured
		// and never joins. It happened, on a rebuild that changed only its
		// size, and the cluster's health check waited out its ten minutes
		// for a node that could not come.
		var replace []string
		if ctx.TakenOverOK && configuresZones(st) {
			var err error
			if replace, err = forgetReplacedUntrustedWorkers(ctx); err != nil {
				return err
			}
		}
		run.Info(st.Say)
		if err := run.TofuApplyReplacing(in, "tofu apply ("+st.Label+")", replace, st.Targets...); err != nil {
			return err
		}
		// The cluster root is finished and every node is ready: a new
		// untrusted worker is given its clean snapshot and opened to work.
		if has.zones && st.Root == config.ClusterRoot && len(st.Targets) == 0 {
			if err := cleanNewUntrustedWorkers(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// rootFor is the context to run tofu in for a root, by its name in the steps.
// Asking for the platform root hands it the cluster's access first: that root
// configures its provider from the cluster root's outputs, so nothing plans
// or applies there before they have been read.
func rootFor(ctx *run.Context, name string, tofu asbuilt.Tofu) (*run.Context, error) {
	switch name {
	case config.ClusterRoot:
		if err := handOverHardware(ctx); err != nil {
			return nil, err
		}
		return ctx.In(ctx.Cluster), nil
	case config.PlatformRoot:
		if err := handOverClusterAccess(ctx, tofu); err != nil {
			return nil, err
		}
		return ctx.In(ctx.Platform), nil
	}
	return nil, fmt.Errorf("a step names the root %q, and a site's roots are %v", name, config.Roots)
}

// platformInputs is steps.PlatformInputs: what passes from the cluster root to
// the platform root.
var platformInputs = steps.PlatformInputs

// handOverClusterAccess reads the cluster root's access to the cluster and
// puts it where every tofu run in the platform root will find it.
//
// In the environment rather than a file: a variable file is one more
// credential on disk for Sterilize to own, and the environment ends with the
// process, as TF_ENCRYPTION already does. The cluster root declares neither
// variable, so tofu ignores them there.
func handOverClusterAccess(ctx *run.Context, tofu asbuilt.Tofu) error {
	vars, err := steps.ClusterAccess(ctx.Cluster.Dir, tofu)
	if err != nil {
		return err
	}
	for name, value := range vars {
		if err := os.Setenv("TF_VAR_"+name, value); err != nil {
			return err
		}
	}
	return nil
}
