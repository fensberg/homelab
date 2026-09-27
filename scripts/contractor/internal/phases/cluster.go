package phases

import (
	"homelab/contractor/config"
	"homelab/contractor/internal/run"
	"homelab/contractor/steps"
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

// applySteps applies one phase's declared steps, in order. A step for workers
// is skipped when the site has none.
func applySteps(ctx *run.Context, phase string, workers bool) error {
	for _, st := range steps.Of(phase) {
		if st.WhenWorkers && !workers {
			continue
		}
		run.Info(st.Say)
		if err := run.TofuApply(ctx, "tofu apply ("+st.Label+")", st.Targets...); err != nil {
			return err
		}
	}
	return nil
}
