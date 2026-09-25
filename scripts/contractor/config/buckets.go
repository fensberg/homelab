package config

import (
	"fmt"
	"strings"

	"homelab/details/cloudflare"
)

// Bucket is one of the estate's object-storage buckets.
//
// WHY THERE IS A TABLE HERE AT ALL, AND NOT JUST FOUR RESOURCES IN HCL.
//
// Three separate parts of the program have to agree about the same set: the
// Cluster phase adopts each bucket that a previous run left behind, the
// Sterilize phase forgets the ones that must outlive a teardown, and the
// Backup phase writes into one of them by name. That agreement used to be
// expressed by repeating the bucket's address as a string literal in each
// place - two copies when there were two buckets, and it would have been eight
// at four. A set restated in eight places is not a set, it is eight chances to
// disagree, and the disagreement is silent: a bucket missing from the forget
// list is simply destroyed by the next teardown, with a successful exit code.
//
// So the set is declared once here and once in object-storage.tf, deliberately
// twice rather than once - the same defence-in-depth the config contract
// already uses, because the HCL is what creates them and the Go is what
// decides their fate, and a single declaration would mean whichever side was
// wrong took the other with it.
// TestTheBucketTableAgreesWithTheHCL refuses any drift between the two.
type Bucket struct {
	// Key is the resource name in object-storage.tf, and the second half of
	// the resource address. It must be a valid HCL identifier: the address is
	// passed to `tofu state rm` and `tofu import`, and a key needing quoting
	// is a key that will one day be quoted wrongly.
	Key string

	// Suffix is appended to the site's slug - `SiteNetwork.Name`, the same
	// value that names every VM. So a site's buckets read `north-street-office-state`
	// beside machines called `north-street-office-cp-100`, and the real name reaches R2
	// and the rendered config without ever reaching git.
	//
	// The slug is the base because the SITE is the isolation boundary. Two
	// sites share one R2 account, so two sites whose buckets collide are two
	// sites writing into each other's state dumps.
	Suffix string

	// Keep says the bucket outlives a teardown.
	//
	// A false here means the bucket describes an estate that is ending, so it
	// is emptied and destroyed with it. A true means it holds something that
	// is meant to still be there afterwards, so Sterilize takes it out of
	// state before the destroy can reach it and the next ignition adopts it
	// back. Getting this wrong in the true direction leaves a bucket nothing
	// tracks; getting it wrong in the false direction destroys the thing
	// somebody was relying on. The second is the one that cannot be undone.
	Keep bool

	// Holds is one line for an operator-facing message, so a log line about a
	// bucket says what is in it rather than only what it is called.
	Holds string
}

// Buckets is every bucket the estate keeps, in the order a reader should meet
// them: the estate's own working data first, then the things that outlive it.
//
// THE ENVIRONMENT SPLIT IS WORKLOAD-ONLY, AND THAT IS NOT AN OVERSIGHT.
//
// There is no `-staging` copy of the state dumps or the database backups,
// because there is no staging platform to dump. CLAUDE.md is explicit that the
// management tier has no staging or production form - one cluster hosts both
// overlays, separated by namespace. So staging and production are a property
// of a workload's data and of nothing else, and a platform bucket that grew an
// environment suffix would be asserting a second platform that does not exist.
//
// WHY EACH ONE IS ITS OWN BUCKET RATHER THAN A PREFIX. R2 API tokens scope per
// bucket; there is no prefix condition on a permanent token, and a token that
// may write may also delete (#94). So a bucket is exactly one blast radius and
// a prefix buys no protection at all. The rule that produced this list: two
// things belong in different buckets if and only if one being compromised must
// not be able to destroy the other.
var Buckets = []Bucket{
	{
		Key:    "database",
		Suffix: "-database",
		Keep:   false,
		Holds:  "the state database's WAL archive and base backups",
	},
	{
		// Separate from the database bucket above, which is the whole point.
		// The state dump exists precisely to survive the database being lost,
		// and while both sat in one bucket the credential that lives
		// permanently in a cluster Secret could delete the break-glass copy
		// along with the backups it was meant to rescue. One compromise took
		// both layers of a two-layer design.
		//
		// Keep is true for the second reason, which is #94: demolish empties
		// the estate's bucket because Cloudflare refuses to delete a
		// non-empty one, so the operation most likely to precede needing a
		// state dump was the one that destroyed every state dump. Eleven
		// objects went that way.
		Key:    "state",
		Suffix: "-state",
		Keep:   true,
		Holds:  "age-encrypted OpenTofu state dumps",
	},
	{
		Key:    "staging",
		Suffix: "-staging",
		Keep:   true,
		Holds:  "staging workload data",
	},
	{
		Key:    "production",
		Suffix: "-production",
		Keep:   true,
		Holds:  "production workload data",
	},
}

// Name is the bucket's real name for a site.
//
// Every bucket is the site slug plus a suffix, with no exceptions - the
// database bucket used to take its name from a config field, which made this
// take a second argument that three of four callers had to pass and ignore.
// The operator retired that by renaming the bucket to carry a suffix like the
// others, which deleted the special case rather than documenting it.
func (b Bucket) Name(site *SiteNetwork) string { return site.Name + b.Suffix }

// Address is the OpenTofu resource address, matching the resource name in
// object-storage.tf. Written here rather than at each call site so a change to
// the resource's name is one edit rather than four.
func (b Bucket) Address() string {
	return "cloudflare_r2_bucket." + b.Key
}

// KeptBuckets is every bucket that must outlive a teardown.
func KeptBuckets() []Bucket {
	kept := make([]Bucket, 0, len(Buckets))
	for _, b := range Buckets {
		if b.Keep {
			kept = append(kept, b)
		}
	}
	return kept
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
func RcloneEnv(acct ObjectStorageAccount, cred ObjectStorageCredential) []string {
	prefix := "RCLONE_CONFIG_" + rcloneRemote + "_"
	return []string{
		prefix + "TYPE=s3",
		prefix + "PROVIDER=Cloudflare",
		prefix + "ACCESS_KEY_ID=" + cred.AccessKeyID,
		prefix + "SECRET_ACCESS_KEY=" + cred.SecretAccessKey,
		prefix + "ENDPOINT=" + cloudflare.R2Endpoint(acct.AccountID),
		prefix + "NO_CHECK_BUCKET=true",
	}
}

// BucketAPIURL is where the vendor's API answers "does this bucket exist".
//
// Declared here because two things build it: the Cluster phase, which adopts a
// bucket only if this answers 200, and the api tier, which exists to prove the
// answer is still 200-or-404. A test building its own copy of the URL would be
// proving something about a request the program does not send.
func BucketAPIURL(accountID, bucket string) string {
	return "https://api.cloudflare.com/client/v4/accounts/" + accountID + "/r2/buckets/" + bucket
}

// StateBackups is where a site's age-encrypted state dumps live, and the
// rclone environment that reaches them.
type StateBackups struct {
	Folder string   // the folder holding every dump, as rclone addresses it
	Latest string   // the pointer object a restore reads first
	Env    []string // rclone configured with the state bucket's own credential
}

// StateBackupLocation resolves StateBackups for a site: the state bucket from
// Buckets, named for the site, reached with its own credential and no other.
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
		"access_key_id":     cred.AccessKeyID,
		"secret_access_key": cred.SecretAccessKey,
	} {
		if strings.TrimSpace(val) == "" {
			return StateBackups{}, fmt.Errorf("sites.%s.object_storage.%s.%s is missing from the rendered config", site, bucket.Key, field)
		}
	}
	net, err := ResolveSiteNetwork(cfg, site)
	if err != nil {
		return StateBackups{}, err
	}
	name := bucket.Name(net)
	return StateBackups{
		Folder: StateBackupPath(name),
		Latest: LatestStateBackupPath(name),
		Env:    RcloneEnv(cfg.ObjectStorage, cred),
	}, nil
}
