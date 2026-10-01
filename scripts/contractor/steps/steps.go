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
// written anywhere else in the contractor. When a root becomes a module, every
// address gains a prefix, and this file is the whole of that edit.
//
// A site is two roots, and a step says which it applies in. The cluster root
// ends with the nodes Ready; the platform root starts there, configured from
// the cluster root's outputs, so the order of the steps is the only edge
// between them.
//
// Public rather than internal so the integration tier, a separate module,
// plans the same sequence the converge applies.
package steps

import (
	"encoding/json"
	"errors"
	"fmt"

	"homelab/contractor/config"
	"homelab/details/asbuilt"
)

// Resource addresses in the cluster root.
const (
	DiskImage          = "proxmox_download_file.talos_disk_image"
	Template           = "proxmox_virtual_environment_vm.talos_template"
	ControlPlanes      = "proxmox_virtual_environment_vm.talos_cp"
	Workers            = "proxmox_virtual_environment_vm.talos_worker"
	ControlPlaneConfig = "talos_machine_configuration_apply.control_plane"
	WorkerConfig       = "talos_machine_configuration_apply.worker"
	Bootstrap          = "talos_machine_bootstrap.this"
	OverlayKey         = "tailscale_tailnet_key.hypervisor[0]"
	ClusterHealth      = "data.talos_cluster_health.this[0]"
)

// Step is one apply of the converge.
type Step struct {
	// Root is the root it applies in, one of config.Roots.
	Root string
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

// Converge is every apply a converge makes, in order. The last step in each
// root applies everything in it, which is why the plan of a root's last step
// is that root's whole plan.
var Converge = []Step{
	// Download the image (API only), build one template per hypervisor from
	// it (the only step needing SSH), then clone from the template - a native
	// Proxmox operation with the provider's own retries, so all the control
	// planes together are safe.
	{Root: config.ClusterRoot, Phase: "compute", Label: "compute: disk image", Say: "creating the disk image", Targets: []string{DiskImage}},
	{Root: config.ClusterRoot, Phase: "compute", Label: "compute: template", Say: "creating the Talos template", Targets: []string{Template}},
	{Root: config.ClusterRoot, Phase: "compute", Label: "compute: vms", Say: "cloning the control-plane VMs", Targets: []string{ControlPlanes}},
	// Separate from the control planes: a worker failing to clone is a
	// capacity problem, a control plane failing to clone stops the cluster
	// existing, and the two should not look alike.
	{Root: config.ClusterRoot, Phase: "compute", Label: "compute: workers", Say: "cloning the worker VMs", Targets: []string{Workers}, WhenWorkers: true},

	{Root: config.ClusterRoot, Phase: "cluster", Label: "talos config", Say: "applying the Talos machine configuration", Targets: []string{ControlPlaneConfig}},
	// Targeted rather than left to the untargeted apply below, which once
	// swept it up by accident; after the control plane and before bootstrap is
	// safe, because a worker retries joining until the API server answers.
	{Root: config.ClusterRoot, Phase: "cluster", Label: "worker config", Say: "applying the worker machine configuration", Targets: []string{WorkerConfig}},
	{Root: config.ClusterRoot, Phase: "cluster", Label: "bootstrap", Say: "bootstrapping etcd", Targets: []string{Bootstrap}},
	// The rest of the cluster root: the cluster's access, Cilium, and the
	// health read that waits for every node to be Ready. That is this root's
	// postcondition and the platform root's precondition, and it is why the
	// platform's resources carry no dependency on it: they are not planned
	// until this apply has returned.
	{Root: config.ClusterRoot, Phase: "cluster", Label: "cluster", Say: "finishing the cluster: its network, and every node Ready"},
	// Everything put on the cluster that Flux cannot put there itself, Flux
	// last. Its provider is configured from the outputs of the step above.
	{Root: config.PlatformRoot, Phase: "cluster", Label: "platform", Say: "creating what Flux needs and installing Flux"},
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

// Plan is the steps of one root as a plan walks them.
func Plan(root string) []asbuilt.PlanStep {
	var out []asbuilt.PlanStep
	for _, s := range Converge {
		if s.Root == root {
			out = append(out, asbuilt.PlanStep{Label: s.Label, Targets: s.Targets})
		}
	}
	return out
}

// PlatformInputs are the platform root's variables that are the cluster
// root's outputs of the same names: how to reach the cluster's API, and the
// same access as a kubeconfig for the Flux bootstrap's kubectl. They are the
// whole of what passes between the two roots.
var PlatformInputs = []string{"cluster_access", "kubeconfig"}

// ClusterAccess reads PlatformInputs from the cluster root in clusterDir, as
// the values tofu takes for the variables: what a run in the platform root is
// given before it plans or applies anything.
func ClusterAccess(clusterDir string, tofu asbuilt.Tofu) (map[string]string, error) {
	raw, _, err := tofu(clusterDir, nil, "output", "-no-color", "-json")
	if err != nil {
		return nil, fmt.Errorf("could not read the cluster root's outputs, so nothing can be put on the cluster yet: %w", err)
	}
	var outputs map[string]any
	if err := json.Unmarshal(raw, &outputs); err != nil {
		return nil, errors.New("the cluster root's outputs did not come back as JSON - its state is probably not reachable from this workspace")
	}
	vars, err := asbuilt.OutputVars(map[string]any{"outputs": outputs}, PlatformInputs...)
	if err != nil {
		return nil, fmt.Errorf("the cluster root has not produced the cluster's access. Has its last step run? (%w)", err)
	}
	return vars, nil
}
