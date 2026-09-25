//go:build api

package api_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"homelab/contractor/config"
	"homelab/tests/harness"
)

// Every bucket the estate granted this site answers to the key granted with
// it. The estate creates the buckets and mints a key per bucket; a key the
// vendor has revoked, or a grant written for a bucket that no longer exists,
// leaves the site configured and unable to store anything, and only the
// vendor can say which.
//
// covers: api:object_storage
func TestEachGrantedKeyReachesItsOwnBucket(t *testing.T) {
	storage := harness.SiteConfig(t).ObjectStorage
	require.NotEmpty(t, storage.AccountID, "the rendered config has no object_storage.account_id")

	for _, bucket := range config.Buckets {
		cred, err := storage.CredentialFor(bucket.Key)
		require.NoError(t, err)
		t.Run(bucket.Key, func(t *testing.T) {
			require.NotEmpty(t, cred.Bucket, "the %s bucket was granted with no name", bucket.Key)
			_, err := harness.RunEnv(t, config.RcloneEnv(storage.AccountID, cred),
				"rclone", "lsf", "--max-depth", "1", config.BucketRemote(cred.Bucket))
			require.NoError(t, err,
				"the key granted for the %s bucket, which holds %s, cannot list it. The estate's "+
					"grant is stale or the key was revoked: a converge-estate rewrites the grant.",
				bucket.Key, bucket.Holds)
		})
	}
}

// The narrowing is the point of the grants: the key that lives permanently in
// a cluster Secret for the database's WAL archive must not reach the state
// dumps, which exist to survive that cluster being lost (#94). Asked of the
// vendor, because a key's scope is a vendor setting nothing here can read.
func TestTheDatabaseKeyCannotReachTheStateDumps(t *testing.T) {
	storage := harness.SiteConfig(t).ObjectStorage
	database, err := storage.CredentialFor("database")
	require.NoError(t, err)
	state, err := storage.CredentialFor("state")
	require.NoError(t, err)

	_, err = harness.RunEnv(t, config.RcloneEnv(storage.AccountID, database),
		"rclone", "lsf", "--max-depth", "1", config.BucketRemote(state.Bucket))
	require.Error(t, err,
		"the database bucket's key can list the state bucket. The key that lives in a cluster "+
			"Secret can then delete the break-glass copy of the state it exists to protect.")
}
