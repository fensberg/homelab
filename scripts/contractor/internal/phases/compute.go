package phases

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	urlpkg "net/url"
	"os"
	"strings"
	"time"

	"homelab/contractor/internal/config"
	"homelab/contractor/internal/onepassword"
	"homelab/contractor/internal/run"
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

	if err := reclaimOrphanedDiskImage(ctx, cfg, net); err != nil {
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

// reclaimOrphanedDiskImage deletes a Talos disk image left behind by a prior
// run's incomplete teardown, so the apply that follows can download it fresh.
//
// It used to import it instead, and that could never have worked. `tofu
// import` configures EVERY provider in the root, not just the one owning the
// resource being imported - and the kubernetes provider here is configured
// from talos_cluster_kubeconfig attributes that do not exist until the cluster
// is built. So an import during Compute fails with "Invalid provider
// configuration ... depends on values that cannot be determined until apply",
// pointing at versions.tf, before it ever reaches the resource in question.
//
// Confirmed by running both against the real estate: `tofu plan -target` of
// this same resource succeeds, because targeting configures only the providers
// the target needs, while `tofu import` of it does not.
//
// Deleting rather than adopting is a fair trade for this resource specifically
// and would not be for others. The image is derived data: re-downloading costs
// a couple of minutes, and compute.tf already accepts an adopt-then-replace
// cycle whenever the stored image came from a different talos_version. A
// bucket holding backups, by contrast, must still be adopted - see
// adoptOrphanedR2Bucket, which runs late enough for import to work.
//
// The expected file name embeds talos_version, which lives only in
// variables.tf; matching by pattern instead avoids keeping a second copy of
// that value in step.
func reclaimOrphanedDiskImage(ctx *run.Context, cfg *config.Config, net *config.SiteNetwork) error {
	site := cfg.Sites[ctx.Site]
	hv := net.Hypervisors[0]
	address := fmt.Sprintf("proxmox_download_file.talos_disk_image[%q]", hv.Hostname)

	// Already tracked: this is an ordinary re-run and the image is ours.
	if run.InState(ctx, address) {
		return nil
	}

	volID, err := findTalosDiskImage(site.Hypervisor, hv)
	if err != nil {
		return fmt.Errorf("checking whether a disk image already exists outside Terraform: %w", err)
	}
	if volID == "" {
		return nil
	}

	run.Info(volID + " exists outside Terraform - a prior run left it behind")
	run.Info("deleting it so this run can download a known-good copy")
	if err := deleteDatastoreFile(site.Hypervisor, hv, volID); err != nil {
		return fmt.Errorf(`could not delete the orphaned disk image %s: %w

Delete it by hand and re-run:

    ssh root@%s "pvesm free %s"`, volID, err, hv.IP, volID)
	}
	run.Ok("orphaned disk image removed")
	return nil
}

// deleteDatastoreFile removes one volume from a Proxmox datastore. The API
// answers with a task id and does the work asynchronously, so this polls until
// the file is actually gone rather than trusting the acknowledgement - a
// download started while the delete is still running fails exactly the way the
// orphan did.
func deleteDatastoreFile(hv config.Hypervisor, node config.Node, volID string) error {
	client := &http.Client{
		Timeout: 30 * time.Second,
		// Same self-signed endpoint versions.tf already accepts with
		// insecure = true; see findTalosDiskImage below.
		// nosemgrep: problem-based-packs.insecure-transport.go-stdlib.bypass-tls-verification.bypass-tls-verification
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}}, //nolint:gosec
	}

	url := datastoreFileURL(node, volID)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", fmt.Sprintf("PVEAPIToken=%s=%s", hv.TokenID, hv.TokenSecret))

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		remaining, err := findTalosDiskImage(hv, node)
		if err == nil && remaining == "" {
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("the delete was accepted but %s is still in the datastore two minutes later", volID)
}

// datastoreContentURL lists what is in the local-iso datastore on a node.
func datastoreContentURL(node config.Node) string {
	return fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/storage/local-iso/content", node.IP, node.Hostname)
}

// datastoreFileURL addresses one volume in that datastore.
//
// PathEscape, not QueryEscape and not raw. A Proxmox volume id looks like
// "local-iso:iso/talos-v1.13.8-nocloud-amd64.iso" - it carries both a colon
// and a slash, and it occupies a single path segment. Left raw, that slash
// would split the segment and address a URL that does not exist; QueryEscape
// would turn the spaces-as-plus rule loose on a path, which is a different
// encoding entirely.
func datastoreFileURL(node config.Node, volID string) string {
	return datastoreContentURL(node) + "/" + urlpkg.PathEscape(volID)
}

// findTalosDiskImage lists the local-iso datastore's content directly via
// the Proxmox API - not through Terraform, which cannot answer "does this
// exist" without already having it in state - and returns the volid of a
// file matching the naming pattern compute.tf uses, or "" if none is there.
func findTalosDiskImage(hv config.Hypervisor, node config.Node) (string, error) {
	client := &http.Client{
		Timeout: 15 * time.Second,
		// InsecureSkipVerify is deliberate, not a bug: this hits the same
		// self-signed Proxmox endpoint versions.tf's own provider config
		// already accepts with insecure = true. Skipping verification here
		// too keeps this one Go call consistent with that existing,
		// already-accepted decision rather than silently enforcing a
		// stricter policy in one code path than the rest of the project does.
		// nosemgrep: problem-based-packs.insecure-transport.go-stdlib.bypass-tls-verification.bypass-tls-verification
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}}, //nolint:gosec
	}

	url := datastoreContentURL(node)
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
