package phases

import (
	"fmt"
	"strings"

	"homelab/contractor/internal/config"
	"homelab/contractor/internal/run"
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

	// Captured rather than streamed. `tofu state rm` echoes "Removed <address>"
	// for each one, and an address is not the safe half of anything here: a
	// `for_each` key comes from the config, so a resource keyed by the
	// hypervisor's name prints a vault value without any attribute being
	// printed at all. The count above is what a reader needs; the addresses
	// are in the error if this fails.
	args := append([]string{"state", "rm"}, addrs...)
	if _, err := run.CmdOutputQuiet(ctx.ClusterDir, "tofu", args...); err != nil {
		run.Warn("could not forget them: " + err.Error())
		run.Warn("The destroy will try to delete them through the Kubernetes API instead, which is what deadlocks on Flux's finalizers. If it hangs, that is why.")
		return
	}
	run.Ok("cluster-internal resources forgotten; they go with the disks")
}

// workloadBucketAddress is the one resource the teardown deliberately loses
// track of.
// forgetKeptBuckets takes every bucket that outlives the estate out of state
// before the destroy runs.
//
// This is the exception to the rule stated above - that forgetting something
// which outlives the VMs leaves a real thing nothing tracks. It does, and for
// these buckets that is the point: the window closes on the next ignition,
// which adopts them back.
//
// Without it the destroy reaches a bucket Cloudflare refuses to delete while
// it holds objects, so the teardown stops part-way with the machines still
// running - which is the failure the emptying below exists to prevent.
// Emptied first, the destroy succeeds and takes the contents with it, which is
// the entire thing these buckets exist not to do: a rebuild is the routine way
// a new Talos version reaches these machines (#97), so "survives a teardown"
// is the normal path rather than an edge case.
//
// ONE FAILURE DOES NOT ABANDON THE REST. Each bucket is forgotten on its own
// and a failure on one is reported and stepped over, because the alternative -
// returning early - would leave the buckets after it in state and destroy
// them. A partial forget loses less than an abandoned one.
func forgetKeptBuckets(ctx *run.Context) {
	kept := config.KeptBuckets()
	if len(kept) == 0 {
		// Not reachable from the table as it stands, and deliberately loud if
		// it ever is: a teardown that keeps nothing is one that deletes the
		// state dumps and every workload's data, and it would otherwise do
		// that in silence.
		run.Warn("no object-storage bucket is marked as surviving a teardown; the destroy will delete all of them")
		return
	}

	list, err := run.CmdOutputQuiet(ctx.ClusterDir, "tofu", "state", "list")
	if err != nil {
		run.Warn("could not list state to find the buckets that must survive: " + err.Error())
		run.Warn("The destroy will try to delete them. Cloudflare refuses to delete a bucket with objects in it, so the teardown will stop there with the machines still running. Do not empty them to get past that - that is the data this is protecting.")
		return
	}

	for _, bucket := range kept {
		if !strings.Contains(list, bucket.Address()) {
			continue
		}

		run.Info(fmt.Sprintf("keeping the bucket holding %s - forgetting it so the destroy leaves it alone", bucket.Holds))
		if _, err := run.CmdOutputQuiet(ctx.ClusterDir, "tofu", "state", "rm", bucket.Address()); err != nil {
			run.Warn(fmt.Sprintf("could not forget the bucket holding %s: %s", bucket.Holds, err.Error()))
			run.Warn("The destroy will try to delete it, and Cloudflare refuses to delete a bucket with objects in it - so the teardown will stop there with the machines still running. Empty it by hand only if you are certain nothing in it is wanted.")
			continue
		}
		run.Ok(fmt.Sprintf("kept the bucket holding %s; the next ignition will adopt it", bucket.Holds))
	}
}

// emptyObjectStorage deletes every object in the site's STATE bucket.
//
// It empties exactly the buckets marked Keep=false in the bucket table, which
// today is the database bucket alone. Every other bucket has a name of its own
// and is never reached by this, because what is in them is meant to outlive
// the estate rather than describe it.
//
// Derived from the table rather than from site.ObjectStorage.Bucket directly.
// Those two happen to be the same string while the database bucket carries an
// empty suffix - so writing the shorter one would work today, keep working
// through review, and start emptying the wrong bucket on the day that suffix
// changes. A rename is exactly the change somebody would make believing it was
// cosmetic.
//
// Cloudflare refuses to delete a bucket that is not empty, and returns that
// refusal as a plain apply error part-way through the destroy - so the first
// real teardown stopped there with the VMs still running. tofu has no notion
// of "empty this first"; the S3 API has no recursive delete; so this is
// rclone, with the same environment-variable configuration the Backup phase
// already uses, and no credentials written to disk.
//
// This no longer deletes the age-encrypted state dumps, and that is the change
// #94 asked for. They used to live in this bucket, so the operation most
// likely to precede needing one was the operation that destroyed every one of
// them - eleven objects went that way. They now have a bucket of their own
// that forgetKeptBuckets releases before this runs.
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
	if strings.TrimSpace(site.ObjectStorage.Bucket) == "" || strings.TrimSpace(site.ObjectStorage.AccessKeyID) == "" {
		run.Warn("no object storage credentials in the rendered config; not emptying the bucket")
		return
	}

	database, err := config.BucketByKey("database")
	if err != nil {
		run.Warn("could not find the bucket to empty: " + err.Error())
		return
	}

	net, err := config.ResolveSiteNetwork(cfg, ctx.Site)
	if err != nil {
		run.Warn("could not resolve the site to name the bucket to empty: " + err.Error())
		return
	}

	store := site.ObjectStorage
	store.Bucket = database.Name(net, site.ObjectStorage.Bucket)

	env := r2Env(cfg.ObjectStorage, store)
	remote := "R2:" + store.Bucket

	// Report before deleting. A bucket that is already empty, or was never
	// created because the run failed early, is not an error - there is simply
	// nothing to do, and the destroy carries on to the VMs either way.
	size, err := run.CmdOutputEnv(ctx.ClusterDir, env, "rclone", "--log-level", "ERROR", "size", remote)
	if err != nil {
		// The bucket is not there to be emptied. That is the normal case when a
		// run failed before object storage was created, and it is not a problem:
		// there is nothing to delete, and the bucket resource that would have
		// held it does not exist either.
		//
		// This used to fall through to the delete below and print "could not
		// empty ... Empty it by hand and re-run", which is alarming, wrong, and
		// lands on the failure path where somebody is already trying to work out
		// what went wrong. If the bucket genuinely exists and is unreachable,
		// destroying it fails loudly a moment later, so nothing is hidden by
		// stopping here.
		run.Info("no object storage to empty")
		return
	}

	summary := strings.Join(strings.Fields(strings.ReplaceAll(size, "\n", " ")), " ")
	if strings.Contains(summary, "Total objects: 0") {
		run.Info("object storage is already empty")
		return
	}
	run.Warn("emptying " + remote + " - " + summary)

	if err := run.CmdEnv(ctx.ClusterDir, env, "rclone", "--log-level", "ERROR", "delete", remote); err != nil {
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
func r2Env(acct config.ObjectStorageAccount, store config.ObjectStorage) []string {
	return []string{
		"RCLONE_CONFIG_R2_TYPE=s3",
		"RCLONE_CONFIG_R2_PROVIDER=Cloudflare",
		"RCLONE_CONFIG_R2_ACCESS_KEY_ID=" + store.AccessKeyID,
		"RCLONE_CONFIG_R2_SECRET_ACCESS_KEY=" + store.SecretAccessKey,
		"RCLONE_CONFIG_R2_ENDPOINT=https://" + acct.AccountID + ".r2.cloudflarestorage.com",
		"RCLONE_CONFIG_R2_NO_CHECK_BUCKET=true",
	}
}
