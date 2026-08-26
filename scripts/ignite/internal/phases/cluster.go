package phases

import "homelab/ignite/internal/run"

// Cluster applies the Talos config, bootstraps etcd, and installs Flux.
func Cluster(ctx *run.Context) error {
	run.WritePhase("Cluster", "Apply Talos config, bootstrap etcd, install Flux.")

	run.Info("applying the Talos machine configuration")
	if err := run.TofuApply(ctx, "tofu apply (talos config)", "talos_machine_configuration_apply.control_plane"); err != nil {
		return err
	}

	run.Info("bootstrapping etcd")
	if err := run.TofuApply(ctx, "tofu apply (bootstrap)", "talos_machine_bootstrap.this"); err != nil {
		return err
	}

	// Everything else, including the Flux bootstrap. Flux goes last because
	// its provider is configured from the kubeconfig the previous steps
	// produce.
	run.Info("installing Flux and finishing the apply")
	if err := run.TofuApply(ctx, "tofu apply (flux)"); err != nil {
		return err
	}

	run.Ok("cluster is up and Flux is reconciling")
	return nil
}
