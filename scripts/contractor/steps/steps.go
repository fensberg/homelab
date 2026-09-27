// Package steps is the one place the contractor writes a resource address,
// and the one declaration of what a converge applies, in order (#497).
//
// The converge applies target by target; a plan used to run one untargeted
// plan instead, and the two disagreed about what a converge does. An
// untargeted plan of a pending rename succeeds where a targeted apply is
// refused, so a green pull request halted every converge on main for a day.
// Now the Compute and Cluster phases walk Converge to apply, and both plans -
// `contractor plan` against a copy of real state, and a pull request's against
// the as-built record - walk the same list to plan. Adding, removing or
// retargeting a step changes all of them at once, and none can be edited
// alone.
//
// Every resource address is a constant here, and tests/go/repo refuses one
// written anywhere else in the contractor. When the cluster root becomes a
// module, every address gains a prefix, and this file is the whole of that
// edit.
//
// Public rather than internal so the integration tier, a separate module,
// plans the same sequence the converge applies.
package steps

import "homelab/details/asbuilt"

// Resource addresses in the cluster root.
const (
	DiskImage          = "proxmox_download_file.talos_disk_image"
	Template           = "proxmox_virtual_environment_vm.talos_template"
	ControlPlanes      = "proxmox_virtual_environment_vm.talos_cp"
	Workers            = "proxmox_virtual_environment_vm.talos_worker"
	ControlPlaneConfig = "talos_machine_configuration_apply.control_plane"
	WorkerConfig       = "talos_machine_configuration_apply.worker"
	Bootstrap          = "talos_machine_bootstrap.this"
	Kubeconfig         = "talos_cluster_kubeconfig.this"
	OverlayKey         = "tailscale_tailnet_key.hypervisor[0]"
	ClusterHealth      = "data.talos_cluster_health.this[0]"
)

// Step is one apply of the converge.
type Step struct {
	// Phase is the phase that applies it.
	Phase string
	// Label names it in logs and in a refused plan.
	Label string
	// Say is what the phase announces before applying it.
	Say string
	// Targets are the addresses it applies; none means everything.
	Targets []string
	// WhenWorkers is a step applied only when the site has workers. A plan
	// includes it regardless: a targeted plan of nothing plans nothing.
	WhenWorkers bool
}

// Converge is every apply a converge makes, in order. The last applies
// everything, which is why the plan of the last step is the whole plan.
var Converge = []Step{
	// Download the image (API only), build one template per hypervisor from
	// it (the only step needing SSH), then clone from the template - a native
	// Proxmox operation with the provider's own retries, so all the control
	// planes together are safe.
	{Phase: "compute", Label: "compute: disk image", Say: "creating the disk image", Targets: []string{DiskImage}},
	{Phase: "compute", Label: "compute: template", Say: "creating the Talos template", Targets: []string{Template}},
	{Phase: "compute", Label: "compute: vms", Say: "cloning the control-plane VMs", Targets: []string{ControlPlanes}},
	// Separate from the control planes: a worker failing to clone is a
	// capacity problem, a control plane failing to clone stops the cluster
	// existing, and the two should not look alike.
	{Phase: "compute", Label: "compute: workers", Say: "cloning the worker VMs", Targets: []string{Workers}, WhenWorkers: true},

	{Phase: "cluster", Label: "talos config", Say: "applying the Talos machine configuration", Targets: []string{ControlPlaneConfig}},
	// Targeted rather than left to the untargeted apply below, which once
	// swept it up by accident; after the control plane and before bootstrap is
	// safe, because a worker retries joining until the API server answers.
	{Phase: "cluster", Label: "worker config", Say: "applying the worker machine configuration", Targets: []string{WorkerConfig}},
	{Phase: "cluster", Label: "bootstrap", Say: "bootstrapping etcd", Targets: []string{Bootstrap}},
	// Before the rest, so the providers reading the kubeconfig configure from
	// a known value.
	{Phase: "cluster", Label: "kubeconfig", Say: "materialising the kubeconfig so the providers that read it can configure", Targets: []string{Kubeconfig}},
	// Everything else, Flux last: its provider is configured from what the
	// steps above produce.
	{Phase: "cluster", Label: "flux", Say: "installing Flux and finishing the apply"},
}

// Of is the steps one phase applies, in order.
func Of(phase string) []Step {
	var out []Step
	for _, s := range Converge {
		if s.Phase == phase {
			out = append(out, s)
		}
	}
	return out
}

// Plan is Converge as a plan walks it.
func Plan() []asbuilt.PlanStep {
	out := make([]asbuilt.PlanStep, len(Converge))
	for i, s := range Converge {
		out[i] = asbuilt.PlanStep{Label: s.Label, Targets: s.Targets}
	}
	return out
}
