package phases

import (
	"fmt"
	"time"

	"homelab/ignite/internal/config"
	"homelab/ignite/internal/run"
)

// Compute creates the Talos VMs, then waits for them to answer.
func Compute(ctx *run.Context) error {
	run.WritePhase("Compute", "Create the Talos VMs, then wait for them to answer.")

	run.Info("tofu init")
	if err := run.TofuInit(ctx); err != nil {
		return err
	}

	// Build the VMs only. Splitting this from the Talos phase means a
	// failure here is obviously a Proxmox problem, not a Talos one.
	run.Info("creating the ISO and the virtual machines")
	if err := run.TofuApply(ctx, "tofu apply (compute)",
		"proxmox_virtual_environment_file.talos_iso",
		"proxmox_virtual_environment_vm.talos_cp",
	); err != nil {
		return err
	}
	run.Ok("VMs created")

	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		return err
	}
	net, err := config.ResolveSiteNetwork(cfg, ctx.Site)
	if err != nil {
		return err
	}

	// The provider returns as soon as Proxmox defines the VM. Talos still
	// has to boot. Poll its API port rather than guessing at a sleep.
	for _, node := range net.NodeIPs {
		run.Info(fmt.Sprintf("waiting for the Talos API on %s:50000 ...", node))
		if !run.WaitForPort(node, 50000, 5*time.Minute, 10*time.Second) {
			return fmt.Errorf(`Talos on %s never came up within 5 minutes.

Open that VM's console in the Proxmox web UI. Talos prints its IP on the
maintenance-mode banner:

  - No IP shown    -> the SDN bridge is down or absent (see the Verify phase).
  - A different IP -> cloud-init did not apply your static address.
  - The correct IP -> the VM is fine and this is a routing problem.`, node)
		}
		run.Ok(node + " is up in maintenance mode")
	}
	return nil
}
