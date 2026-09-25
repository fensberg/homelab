package phases

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"homelab/contractor/config"
	"homelab/contractor/internal/run"
	"homelab/details/cloudflare"
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

	// Before the bucket adopt, not after, and not optional.
	//
	// adoptOrphanedR2Buckets shells out to `tofu import`, and import configures
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

	if err := adoptOrphanedR2Buckets(ctx); err != nil {
		return err
	}
	if err := adoptEnrollmentApp(ctx); err != nil {
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

// adoptOrphanedR2Buckets imports any estate bucket that already exists.
//
// Two different situations bring a bucket here, and the distinction is worth
// keeping because it decides how alarming a hit is.
//
// A bucket with Keep=false is found because a teardown FAILED part-way and
// left it behind. That is the recovery case.
//
// A bucket with Keep=true is found because a teardown SUCCEEDED: Sterilize
// forgets those deliberately so the destroy cannot take them with it, which
// makes finding them here the NORMAL case rather than the exceptional one.
//
// If this ever stops working the symptom is a second bucket appearing beside
// the first with a name Cloudflare had to disambiguate, and a workload
// restoring from an empty one.
//
// See run.AdoptIfOrphaned for why this is Go and not a `.tf` import block.
func adoptOrphanedR2Buckets(ctx *run.Context) error {
	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		return err
	}
	// ResolveSiteNetwork is the only lookup needed here, and it refuses an
	// unknown site itself - so there is no separate existence check to drift
	// out of step with it.
	net, err := config.ResolveSiteNetwork(cfg, ctx.Site)
	if err != nil {
		return err
	}

	// Every bucket, including the two nothing holds an S3 credential for.
	//
	// This asks the Cloudflare API with the account's admin token rather than
	// speaking S3, so it needs no per-bucket credential at all - which is what
	// lets staging and production be adopted before anything is given a key to
	// write to them.
	for _, bucket := range config.Buckets {
		name := bucket.Name(net)

		// Before the adopt, not after. A bucket's name is derived from the
		// site slug, so a site renamed in the vault renames every bucket -
		// and the provider cannot rename one in place, so the apply would
		// plan destroy-and-create and then fail on a destroy the vendor
		// refuses. Releasing first lets the adopt below pick up the real one.
		if err := run.ReleaseIfRenamed(ctx, bucket.Address(), name); err != nil {
			return fmt.Errorf("releasing the old bucket that held %s: %w", bucket.Holds, err)
		}

		if err := run.AdoptIfOrphaned(ctx, bucket.Address(), func() (string, error) {
			exists, err := r2BucketExists(cfg.ObjectStorage, name)
			if err != nil || !exists {
				return "", err
			}
			return cfg.ObjectStorage.AccountID + "/" + name, nil
		}); err != nil {
			return fmt.Errorf("adopting the bucket holding %s: %w", bucket.Holds, err)
		}
	}
	return nil
}

// r2BucketExists queries the Cloudflare API directly - not through
// Terraform, which cannot answer "does this exist" without already having
// it in state.
func r2BucketExists(acct config.ObjectStorageAccount, bucket string) (bool, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	url := config.BucketAPIURL(acct.AccountID, bucket)
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

// adoptEnrollmentApp imports the organisation's device-enrollment application
// when it exists, and leaves OpenTofu to create it when it does not.
//
// It was a data source and an import block in tunnel.tf, and that is exactly
// the shape run.AdoptIfOrphaned exists to avoid: an import block hard-fails
// when its target is missing, and so does a data source. Both were fine while
// the application always existed - until a teardown destroyed it, and every
// OpenTofu command after that failed evaluating the data source, including the
// bucket import that happened to run first.
//
// Found by type rather than by name. Cloudflare allows one `warp` application
// per organisation, so the type identifies it, and the name stays written once,
// in tunnel.tf.
func adoptEnrollmentApp(ctx *run.Context) error {
	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		return err
	}
	account := cfg.ObjectStorage.AccountID
	client := &http.Client{Timeout: 15 * time.Second}
	return run.AdoptIfOrphaned(ctx, config.EnrollmentAppAddress, func() (string, error) {
		id, err := findEnrollmentApp(client, config.AccessAppsAPIURL(account), cfg.Tunnel.APIToken)
		if err != nil || id == "" {
			return "", err
		}
		return account + "/" + id, nil
	})
}

// findEnrollmentApp returns the id of the account's `warp` Access application,
// or "" when it has none. Any answer that is not a readable list is an error:
// "could not tell" must never read as "there is none", because the apply would
// then try to create a second one and Cloudflare would refuse.
func findEnrollmentApp(client *http.Client, url, token string) (string, error) {
	for page := 1; ; page++ {
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s?per_page=100&page=%d", url, page), nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("listing Access applications: %w", err)
		}
		var body cloudflare.Answer[[]struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}]
		decodeErr := json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("listing Access applications: HTTP %d", resp.StatusCode)
		}
		if decodeErr != nil || !body.Success {
			return "", fmt.Errorf("listing Access applications: the answer was not a list (%v)", decodeErr)
		}
		for _, app := range body.Result {
			if app.Type == "warp" {
				return app.ID, nil
			}
		}
		if page >= body.ResultInfo.TotalPages {
			return "", nil
		}
	}
}
