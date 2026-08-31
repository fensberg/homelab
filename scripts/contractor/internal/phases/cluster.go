package phases

import (
	"fmt"
	"net/http"
	"time"

	"homelab/contractor/internal/config"
	"homelab/contractor/internal/run"
)

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

	// Before the bucket adopt, not after, and not optional.
	//
	// adoptOrphanedR2Bucket shells out to `tofu import`, and import configures
	// EVERY provider in the root - including the kubernetes provider, which
	// versions.tf configures from this very resource's attributes. Until
	// talos_cluster_kubeconfig.this is in state those attributes are unknown,
	// so the import fails with "Invalid provider configuration" pointing at
	// versions.tf rather than at anything to do with the bucket. Materialising
	// it first is what makes the import below possible at all; the same
	// mechanism made the disk-image adopt in the Compute phase impossible,
	// which is why that one deletes instead - see reclaimOrphanedDiskImage.
	run.Info("materialising the kubeconfig so the providers that read it can configure")
	if err := run.TofuApply(ctx, "tofu apply (kubeconfig)", "talos_cluster_kubeconfig.this"); err != nil {
		return err
	}

	if err := adoptOrphanedR2Bucket(ctx); err != nil {
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

// adoptOrphanedR2Bucket imports the R2 bucket if a prior run's incomplete
// teardown already left it behind. See run.AdoptIfOrphaned for why this is
// Go and not a `.tf` import block.
func adoptOrphanedR2Bucket(ctx *run.Context) error {
	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		return err
	}
	site := cfg.Sites[ctx.Site]

	return run.AdoptIfOrphaned(ctx, "cloudflare_r2_bucket.homelab", func() (string, error) {
		exists, err := r2BucketExists(cfg.ObjectStorage, site.ObjectStorage)
		if err != nil || !exists {
			return "", err
		}
		return cfg.ObjectStorage.AccountID + "/" + site.ObjectStorage.Bucket, nil
	})
}

// r2BucketExists queries the Cloudflare API directly - not through
// Terraform, which cannot answer "does this exist" without already having
// it in state.
func r2BucketExists(acct config.ObjectStorageAccount, os config.ObjectStorage) (bool, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/r2/buckets/%s", acct.AccountID, os.Bucket)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+acct.AdminToken)

	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("querying the R2 bucket: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		// Confirmed against the real API, not assumed: a missing bucket is
		// a genuine 404 ({"errors":[{"code":10006,"message":"The specified
		// bucket does not exist."}]}), not a 200 with an error body.
		return false, nil
	default:
		return false, fmt.Errorf("querying the R2 bucket: HTTP %d", resp.StatusCode)
	}
}
