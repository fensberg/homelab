package phases

import (
	"fmt"
	"net/http"
	"time"

	"homelab/ignite/internal/config"
	"homelab/ignite/internal/run"
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
		exists, err := r2BucketExists(site.ObjectStorage)
		if err != nil || !exists {
			return "", err
		}
		return site.ObjectStorage.AccountID + "/" + site.ObjectStorage.Bucket, nil
	})
}

// r2BucketExists queries the Cloudflare API directly - not through
// Terraform, which cannot answer "does this exist" without already having
// it in state.
func r2BucketExists(os config.ObjectStorage) (bool, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/r2/buckets/%s", os.AccountID, os.Bucket)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+os.AdminToken)

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
