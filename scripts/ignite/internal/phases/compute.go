package phases

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"homelab/ignite/internal/config"
	"homelab/ignite/internal/onepassword"
	"homelab/ignite/internal/run"
)

// Compute creates the Talos VMs, then waits for them to answer.
func Compute(ctx *run.Context) error {
	run.WritePhase("Compute", "Create the Talos VMs, then wait for them to answer.")

	// Resolved directly with a single op read per value, not through
	// config/management.rendered.json's whole-template inject. The
	// Hypervisor phase is what creates this credential (hypervisor-prep.yml
	// generates it and writes it to 1Password), and that phase runs after
	// Render - templating it alongside everything else would make Render
	// depend on a phase that has not run yet on a brand new site. By the
	// time Compute runs, Hypervisor already has, so this always resolves.
	// See versions.tf for the provider side.
	//
	// The Proxmox API token itself stays in the ordinary rendered config
	// (versions.tf), not this pattern, even though hypervisor-prep.yml can
	// also rotate it (deleting and replacing an orphaned token). Unlike the
	// SSH credential, the token is needed by the Overlay phase too, which
	// runs before Hypervisor - resolving it here would leave Overlay's own
	// tofu apply with no credential at all. The token is meant to already
	// exist before a full run starts; a rotation happening mid-run is a
	// recovery-path edge case; re-running ignite once picks up the
	// now-stable value from a fresh Render.
	run.Info("resolving the disk-import SSH credential")
	sshUser, err := onepassword.Read(fmt.Sprintf("op://homelab/%s/hypervisor/ssh_username", ctx.Site))
	if err != nil {
		return fmt.Errorf("resolving disk-import SSH credential (run the Hypervisor phase first): %w", err)
	}
	sshKey, err := onepassword.Read(fmt.Sprintf("op://homelab/%s/hypervisor/ssh_private_key", ctx.Site))
	if err != nil {
		return fmt.Errorf("resolving disk-import SSH credential (run the Hypervisor phase first): %w", err)
	}
	if err := os.Setenv("PROXMOX_VE_SSH_USERNAME", sshUser); err != nil {
		return err
	}
	if err := os.Setenv("PROXMOX_VE_SSH_PRIVATE_KEY", sshKey); err != nil {
		return err
	}

	run.Info("tofu init")
	if err := run.TofuInit(ctx); err != nil {
		return err
	}

	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		return err
	}
	net, err := config.ResolveSiteNetwork(cfg, ctx.Site)
	if err != nil {
		return err
	}

	if err := adoptOrphanedDiskImage(ctx, cfg, net); err != nil {
		return err
	}

	// Three steps, not one apply targeting everything: download the image
	// (API only), build one template per hypervisor from it (the only step
	// that needs SSH - see compute.tf's talos_template resource for why
	// that's now confined to a single import instead of one per node), then
	// clone the real control-plane VMs from that template. Cloning is a
	// native, API-only Proxmox operation the provider documents built-in
	// retries for, so - unlike the file_id import this replaced - it is
	// safe to apply all of them together in one call.
	run.Info("creating the disk image")
	if err := run.TofuApply(ctx, "tofu apply (compute: disk image)",
		"proxmox_download_file.talos_disk_image",
	); err != nil {
		return err
	}
	run.Info("creating the Talos template")
	if err := run.TofuApply(ctx, "tofu apply (compute: template)",
		"proxmox_virtual_environment_vm.talos_template",
	); err != nil {
		return err
	}
	run.Info(fmt.Sprintf("cloning %d control-plane VM(s)", len(net.NodeIPs)))
	if err := run.TofuApply(ctx, "tofu apply (compute: vms)",
		"proxmox_virtual_environment_vm.talos_cp",
	); err != nil {
		return err
	}
	run.Ok("VMs created")

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

// adoptOrphanedDiskImage imports the Talos disk image if a prior run's
// incomplete teardown already left it sitting in the datastore. See
// run.AdoptIfOrphaned for why this is Go and not a `.tf` import block.
//
// The exact expected file_name embeds talos_version, which lives only in
// variables.tf - duplicating it here would be a second copy of that value
// to keep in sync. Matching by pattern and importing whatever real filename
// the datastore actually reports sidesteps that: if it happens to be a
// stale image from a previous talos_version, the url/decompression_algorithm
// mismatch already documented in compute.tf forces an immediate replace
// anyway, so the outcome still converges - just with one adopt-then-replace
// cycle instead of a clean adopt, the same tradeoff already known and
// accepted for this resource.
func adoptOrphanedDiskImage(ctx *run.Context, cfg *config.Config, net *config.SiteNetwork) error {
	site := cfg.Sites[ctx.Site]
	hv := net.Hypervisors[0]
	address := fmt.Sprintf("proxmox_download_file.talos_disk_image[%q]", hv.Hostname)

	return run.AdoptIfOrphaned(ctx, address, func() (string, error) {
		volID, err := findTalosDiskImage(site.Hypervisor, hv)
		if err != nil || volID == "" {
			return "", err
		}
		// bpg/proxmox's own import ID shape: node_name/datastore_id:content_type/file_name
		// - volID from the API is already "datastore_id:content_type/file_name".
		return hv.Hostname + "/" + volID, nil
	})
}

// findTalosDiskImage lists the local-iso datastore's content directly via
// the Proxmox API - not through Terraform, which cannot answer "does this
// exist" without already having it in state - and returns the volid of a
// file matching the naming pattern compute.tf uses, or "" if none is there.
func findTalosDiskImage(hv config.Hypervisor, node config.Node) (string, error) {
	client := &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // matches versions.tf's own insecure=true for this same self-signed homelab endpoint
	}

	url := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/storage/local-iso/content", node.IP, node.Hostname)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", fmt.Sprintf("PVEAPIToken=%s=%s", hv.TokenID, hv.TokenSecret))

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("querying the local-iso datastore: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("querying the local-iso datastore: HTTP %d: %s", resp.StatusCode, body)
	}

	var parsed struct {
		Data []struct {
			VolID string `json:"volid"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("parsing the datastore content response: %w", err)
	}

	for _, item := range parsed.Data {
		// "local-iso:iso/talos-v1.13.8-nocloud-amd64.iso" - the exact
		// version segment doesn't matter here, see the doc comment above.
		if strings.HasPrefix(item.VolID, "local-iso:iso/talos-") && strings.HasSuffix(item.VolID, "-nocloud-amd64.iso") {
			return item.VolID, nil
		}
	}
	return "", nil
}
