package config

import (
	"fmt"
	"strings"

	"homelab/details/cloudflare"
)

// Bucket is one of a site's object-storage buckets, by purpose.
//
// The estate creates a site's buckets, names them, and grants the site a key
// for each (management/estate/site/object-storage.tf). So this is no longer a
// declaration of what to create, adopt or keep: a site cannot create, delete
// or adopt a bucket at all, because it holds no credential that could. What a
// site still has to know is which bucket is which, and which one its teardown
// empties. TestTheBucketTableAgreesWithTheHCL holds this list to the estate's.
type Bucket struct {
	// Key is the bucket's purpose, as the estate names it and as the site's
	// object_storage config is keyed.
	Key string

	// EmptiedAtTeardown says a site teardown empties the bucket with the
	// site's own key. The database bucket describes a cluster that is ending:
	// CloudNativePG refuses to archive into a bucket holding another cluster's
	// WAL, so the next build of the site needs it empty. Everything else holds
	// what is meant to outlive the cluster.
	EmptiedAtTeardown bool

	// Holds is one line for an operator-facing message, so a log line about a
	// bucket says what is in it rather than only what it is called.
	Holds string
}

// Buckets is every bucket a site is granted, in the order a reader should
// meet them: the cluster's own working data first, then what outlives it.
//
// THE ENVIRONMENT SPLIT IS WORKLOAD-ONLY, AND THAT IS NOT AN OVERSIGHT.
//
// There is no `-staging` copy of the state dumps or the database backups,
// because there is no staging platform to dump. CLAUDE.md is explicit that the
// management tier has no staging or production form - one cluster hosts both
// overlays, separated by namespace. So staging and production are a property
// of a workload's data and of nothing else.
//
// WHY EACH ONE IS ITS OWN BUCKET RATHER THAN A PREFIX. R2 API tokens scope per
// bucket; there is no prefix condition on a permanent token, and a token that
// may write may also delete (#94). So a bucket is exactly one blast radius and
// a prefix buys no protection at all.
var Buckets = []Bucket{
	{Key: "database", EmptiedAtTeardown: true, Holds: "the state database's WAL archive and base backups"},
	// Separate from the database bucket, which is the whole point: the state
	// dump exists to survive the database being lost, and while both sat in
	// one bucket the key that lives in a cluster Secret could delete the
	// break-glass copy along with the backups it was meant to rescue.
	{Key: "state", Holds: "age-encrypted OpenTofu state dumps"},
	{Key: "staging", Holds: "staging workload data"},
	{Key: "production", Holds: "production workload data"},
}

// BucketByKey finds a bucket by its key.
//
// Returns an error rather than a bool, because every caller here is naming a
// bucket it believes exists - a miss is a programming mistake, and the message
// is more useful than a second return value nobody prints.
func BucketByKey(key string) (Bucket, error) {
	for _, b := range Buckets {
		if b.Key == key {
			return b, nil
		}
	}
	return Bucket{}, fmt.Errorf("no object-storage bucket is declared with the key %q", key)
}

// Where things live inside object storage, declared once.
//
// These were spelled out separately by the Backup phase, the Restore phase,
// the teardown, the destroy report and the integration tier's backup checks.
// Backup and restore are the two ends of one pipe, and they only agreed because
// two people typed the same folder name - a guard then policed that they still
// did. One declaration removes the thing the guard was watching.

// rcloneRemote is the name RcloneEnv gives the remote. Paths below are written
// against it, so the two are one fact rather than two strings that must match.
const rcloneRemote = "R2"

// StateBackupFolder holds the age-encrypted state dumps inside the state
// bucket. A folder rather than the bucket root because the provider split will
// give this site two states, each wanting its own.
const StateBackupFolder = "management-cluster"

// LatestStateBackup is the pointer object, overwritten on every backup and
// never pruned; the timestamped objects beside it are the history.
const LatestStateBackup = "latest.tfstate.age"

// BucketRemote is a bucket as rclone addresses it through RcloneEnv.
func BucketRemote(bucket string) string { return rcloneRemote + ":" + bucket }

// StateBackupPath is the folder holding a bucket's state backups.
func StateBackupPath(bucket string) string {
	return BucketRemote(bucket) + "/" + StateBackupFolder
}

// LatestStateBackupPath is the object a restore reads first.
func LatestStateBackupPath(bucket string) string {
	return StateBackupPath(bucket) + "/" + LatestStateBackup
}

// RcloneEnv configures rclone entirely through environment variables scoped
// to one process, so no credential is ever written to a config file on disk.
//
// Takes one credential rather than a site's whole object-storage block,
// because there is no longer one credential for everything - passing the block
// would leave each caller picking a pair out of it, which is the decision this
// signature takes away from them.
func RcloneEnv(accountID string, cred ObjectStorageCredential) []string {
	prefix := "RCLONE_CONFIG_" + rcloneRemote + "_"
	return []string{
		prefix + "TYPE=s3",
		prefix + "PROVIDER=Cloudflare",
		prefix + "ACCESS_KEY_ID=" + cred.AccessKeyID,
		prefix + "SECRET_ACCESS_KEY=" + cred.SecretAccessKey,
		prefix + "ENDPOINT=" + cloudflare.R2Endpoint(accountID),
		prefix + "NO_CHECK_BUCKET=true",
	}
}

// StateBackups is where a site's age-encrypted state dumps live, and the
// rclone environment that reaches them.
type StateBackups struct {
	Folder string   // the folder holding every dump, as rclone addresses it
	Latest string   // the pointer object a restore reads first
	Env    []string // rclone configured with the state bucket's own credential
}

// StateBackupLocation resolves StateBackups for a site: the state bucket the
// estate granted it, reached with that bucket's own key and no other.
//
// The Backup phase, the Restore phase and the integration tier each ran this
// sequence for themselves - find the bucket, pick its credential, check it is
// not empty, name it for the site, build the environment. Backup and Restore
// are the two ends of one pipe, and a guard read both files to check they still
// agreed. They had already drifted: Backup refused an empty key id or secret,
// Restore only the key id. One function leaves nothing to agree.
func StateBackupLocation(cfg *Config, site string) (StateBackups, error) {
	bucket, err := BucketByKey("state")
	if err != nil {
		return StateBackups{}, err
	}
	s, ok := cfg.Sites[site]
	if !ok {
		return StateBackups{}, fmt.Errorf("unknown site '%s'", site)
	}
	cred, err := s.ObjectStorage.CredentialFor(bucket.Key)
	if err != nil {
		return StateBackups{}, err
	}
	for field, val := range map[string]string{
		"bucket":            cred.Bucket,
		"access_key_id":     cred.AccessKeyID,
		"secret_access_key": cred.SecretAccessKey,
	} {
		if strings.TrimSpace(val) == "" {
			return StateBackups{}, fmt.Errorf("sites.%s.object_storage.%s.%s is missing from the rendered config", site, bucket.Key, field)
		}
	}
	if strings.TrimSpace(s.ObjectStorage.AccountID) == "" {
		return StateBackups{}, fmt.Errorf("sites.%s.object_storage.account_id is missing from the rendered config", site)
	}
	return StateBackups{
		Folder: StateBackupPath(cred.Bucket),
		Latest: LatestStateBackupPath(cred.Bucket),
		Env:    RcloneEnv(s.ObjectStorage.AccountID, cred),
	}, nil
}
