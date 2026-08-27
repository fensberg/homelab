package phases

import (
	"fmt"
	"strings"

	"homelab/ignite/internal/config"
	"homelab/ignite/internal/run"
)

// The two things `tofu destroy` cannot do for itself, discovered the only way
// this kind of thing ever is - by running a real teardown against a real
// estate and watching it destroy exactly nothing. See teardown_test.go for
// both failures in full.

// clusterInternalAddresses picks the state entries whose remote objects live
// inside the cluster's own VMs, so the teardown can forget them rather than
// politely delete them.
//
// That is not a shortcut around a slow API call. Deleting the flux-system
// namespace blocks on Flux's finalizers, and the controllers that would clear
// those finalizers are themselves being torn down - so the delete waits until
// the provider's context expires and the destroy aborts having destroyed
// nothing, including the VMs. Asking a dying cluster to tidy up before you
// delete its disks is the whole mistake.
//
// Selection is by provider prefix rather than by a hardcoded list of the six
// resources that exist today, so adding a namespace or a secret to
// database.tf or gitops.tf does not quietly reintroduce the deadlock.
// Everything with a remote object that outlives the VMs - the R2 bucket, the
// tailnet key, the VMs themselves - is deliberately not matched: forgetting
// one of those leaves a real thing running that nothing tracks, which is far
// worse than the hang this avoids.
func clusterInternalAddresses(stateList string) []string {
	var out []string
	for _, line := range strings.Split(stateList, "\n") {
		addr := strings.TrimSpace(line)
		if addr == "" || strings.HasPrefix(addr, "data.") {
			continue
		}
		if strings.HasPrefix(addr, "kubernetes_") {
			out = append(out, addr)
		}
	}
	return out
}

// forgetClusterInternalResources removes those addresses from state.
//
// Best-effort on purpose. A failure here is a reason to warn and carry on to
// the part of the destroy that removes real infrastructure, not a reason to
// stop: the worst case is that tofu tries the graceful delete and hangs, which
// is exactly where this started.
func forgetClusterInternalResources(ctx *run.Context) {
	list, err := run.CmdOutputQuiet(ctx.ClusterDir, "tofu", "state", "list")
	if err != nil {
		run.Warn("could not list state to find cluster-internal resources: " + err.Error())
		return
	}
	addrs := clusterInternalAddresses(list)
	if len(addrs) == 0 {
		return
	}

	run.Info(fmt.Sprintf("forgetting %d resource(s) that live inside the VMs about to be deleted", len(addrs)))
	args := append([]string{"state", "rm"}, addrs...)
	if err := run.Cmd(ctx.ClusterDir, "tofu", args...); err != nil {
		run.Warn("could not forget them: " + err.Error())
		run.Warn("The destroy will try to delete them through the Kubernetes API instead, which is what deadlocks on Flux's finalizers. If it hangs, that is why.")
		return
	}
	run.Ok("cluster-internal resources forgotten; they go with the disks")
}

// emptyObjectStorage deletes every object in the site's bucket.
//
// Cloudflare refuses to delete a bucket that is not empty, and returns that
// refusal as a plain apply error part-way through the destroy - so the first
// real teardown stopped there with the VMs still running. tofu has no notion
// of "empty this first"; the S3 API has no recursive delete; so this is
// rclone, with the same environment-variable configuration the Backup phase
// already uses, and no credentials written to disk.
//
// This deletes the age-encrypted state backups along with everything else,
// which is correct and worth saying out loud: they describe an estate that is
// about to stop existing. The state itself has already been migrated back to
// local disk by the time this runs, so it is not the last copy of anything
// the rest of the teardown still needs.
func emptyObjectStorage(ctx *run.Context) {
	cfg, err := config.LoadRendered(ctx.ConfigRendered)
	if err != nil {
		run.Warn("could not read the rendered config to empty object storage: " + err.Error())
		return
	}
	site, ok := cfg.Sites[ctx.Site]
	if !ok {
		run.Warn("no site " + ctx.Site + " in the rendered config; not emptying object storage")
		return
	}
	store := site.ObjectStorage
	if strings.TrimSpace(store.Bucket) == "" || strings.TrimSpace(store.AccessKeyID) == "" {
		run.Warn("no object storage credentials in the rendered config; not emptying the bucket")
		return
	}

	env := r2Env(store)
	remote := "R2:" + store.Bucket

	// Report before deleting. A bucket that is already empty, or was never
	// created because the run failed early, is not an error - there is simply
	// nothing to do, and the destroy carries on to the VMs either way.
	if size, err := run.CmdOutputEnv(ctx.ClusterDir, env, "rclone", "size", remote); err == nil {
		summary := strings.Join(strings.Fields(strings.ReplaceAll(size, "\n", " ")), " ")
		if strings.Contains(summary, "Total objects: 0") {
			run.Info("object storage is already empty")
			return
		}
		run.Warn("emptying " + remote + " - " + summary)
	}

	if err := run.CmdEnv(ctx.ClusterDir, env, "rclone", "delete", remote); err != nil {
		run.Warn("could not empty " + remote + ": " + err.Error())
		run.Warn("Cloudflare refuses to delete a bucket with objects in it, so the destroy will stop there. Empty it by hand and re-run.")
		return
	}
	run.Ok("object storage emptied")
}

// r2Env configures rclone entirely through environment variables scoped to
// this process, so no credentials are ever written to a config file on disk.
// Shared by the Backup phase and the teardown: two copies of a credential
// mapping is two places for a rename to go unnoticed.
func r2Env(store config.ObjectStorage) []string {
	return []string{
		"RCLONE_CONFIG_R2_TYPE=s3",
		"RCLONE_CONFIG_R2_PROVIDER=Cloudflare",
		"RCLONE_CONFIG_R2_ACCESS_KEY_ID=" + store.AccessKeyID,
		"RCLONE_CONFIG_R2_SECRET_ACCESS_KEY=" + store.SecretAccessKey,
		"RCLONE_CONFIG_R2_ENDPOINT=https://" + store.AccountID + ".r2.cloudflarestorage.com",
		"RCLONE_CONFIG_R2_NO_CHECK_BUCKET=true",
	}
}
