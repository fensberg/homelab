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
	if err := applySteps(ctx, "cluster", len(net.WorkerIPs) > 0); err != nil {
		return err
	}
	run.Ok("cluster is up and Flux is reconciling")
	return nil
}

// applySteps applies one phase's declared steps, in order, each in the root
// it names. A step for workers is skipped when the site has none.
func applySteps(ctx *run.Context, phase string, workers bool) error {
	for _, st := range steps.Of(phase) {
		if st.WhenWorkers && !workers {
			continue
		}
		in, err := rootFor(ctx, st.Root, asbuilt.Exec)
		if err != nil {
			return err
		}
		run.Info(st.Say)
		if err := run.TofuApply(in, "tofu apply ("+st.Label+")", st.Targets...); err != nil {
			return err
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
