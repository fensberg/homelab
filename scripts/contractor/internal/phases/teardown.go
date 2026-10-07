package phases

import (
	"strings"

	"homelab/contractor/config"
	"homelab/contractor/internal/run"
)

// The thing `tofu destroy` cannot do for itself, discovered the only way this
// kind of thing ever is - by running a real teardown against a real estate
// and watching it destroy exactly nothing.

// workloadBucketAddress is the one resource the teardown deliberately loses
// track of.

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
// them - eleven objects went that way. They now have a bucket of their own,
// and a site teardown reaches no bucket but this one: every bucket is the
// estate's, and the site's key for this one can empty it and nothing more.
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
	database, err := config.BucketByKey("database")
	if err != nil {
		run.Warn("could not find the bucket to empty: " + err.Error())
		return
	}

	cred, err := site.ObjectStorage.CredentialFor(database.Key)
	if err != nil {
		run.Warn("not emptying the bucket: " + err.Error())
		return
	}
	if strings.TrimSpace(cred.AccessKeyID) == "" {
		run.Warn("no object storage credential in the rendered config; not emptying the bucket")
		return
	}

	bucketName := cred.Bucket
	if strings.TrimSpace(bucketName) == "" {
		run.Warn("no name for the " + database.Key + " bucket in the rendered config; not emptying it")
		return
	}
	env := config.RcloneEnv(site.ObjectStorage.AccountID, cred)
	remote := config.BucketRemote(bucketName)

	// Report before deleting. A bucket that is already empty, or was never
	// created because the run failed early, is not an error - there is simply
	// nothing to do, and the destroy carries on to the VMs either way.
	size, err := run.CmdOutputEnv(ctx.Dir, env, "rclone", "--log-level", "ERROR", "size", remote)
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
	// By its purpose, not its name: a bucket is named for the organisation
	// and the site (#651).
	run.Warn("emptying the " + database.Key + " bucket - " + summary)

	if err := run.CmdEnv(ctx.Dir, env, "rclone", "--log-level", "ERROR", "delete", remote); err != nil {
		run.Warn("could not empty " + remote + ": " + err.Error())
		run.Warn("Cloudflare refuses to delete a bucket with objects in it, so the destroy will stop there. Empty it by hand and re-run.")
		return
	}
	run.Ok("object storage emptied")
}
