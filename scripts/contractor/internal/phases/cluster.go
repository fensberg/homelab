package phases

import (
	"homelab/contractor/internal/run"
)

// Cluster applies the Talos config, bootstraps etcd, and installs Flux.
func Cluster(ctx *run.Context) error {
	run.WritePhase("Cluster", "Apply Talos config, bootstrap etcd, install Flux.")

	run.Info("applying the Talos machine configuration")
	if err := run.TofuApply(ctx, "tofu apply (talos config)", "talos_machine_configuration_apply.control_plane"); err != nil {
		return err
	}

	// Targeted, not left to the untargeted apply further down.
	//
	// It did work by accident: the final `tofu apply (flux)` has no target, so
	// it swept the worker configuration up and the cluster came out right. But
	// "correct incidentally" is the kind of thing that changes when somebody
	// adds a target to that step, and the failure would be workers sitting in
	// maintenance mode while the health gate waits for them.
	//
	// After the control plane and before bootstrap is safe: applying a worker's
	// configuration only writes it to the machine, and the worker retries
	// joining until the API server answers.
	run.Info("applying the worker machine configuration")
	if err := run.TofuApply(ctx, "tofu apply (worker config)", "talos_machine_configuration_apply.worker"); err != nil {
		return err
	}

	run.Info("bootstrapping etcd")
	if err := run.TofuApply(ctx, "tofu apply (bootstrap)", "talos_machine_bootstrap.this"); err != nil {
		return err
	}

	// The kubeconfig before the rest, so the providers that read it configure
	// from a known value. It used to exist for the bucket adopt that followed
	// it - `tofu import` configures every provider in the root - and the
	// buckets are the estate's now, so a site has nothing to adopt. It stays
	// because the apply below has only ever run with it in state.
	run.Info("materialising the kubeconfig so the providers that read it can configure")
	if err := run.TofuApply(ctx, "tofu apply (kubeconfig)", "talos_cluster_kubeconfig.this"); err != nil {
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
