package config

import "fmt"

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

	// Suffix is appended to the site's configured bucket name. The base name
	// is a vault value, so a fork changes one field and gets a whole set.
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
		Suffix: "",
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

// Name is the bucket's real name, given the site's configured base name.
func (b Bucket) Name(base string) string { return base + b.Suffix }

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
