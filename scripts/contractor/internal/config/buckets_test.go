package config

import (
	"regexp"
	"strings"
	"testing"
)

func TestBucketNameIsTheBaseNamePlusTheSuffix(t *testing.T) {
	for _, tc := range []struct {
		key  string
		base string
		want string
	}{
		{"database", "example", "example"},
		{"state", "example", "example-state"},
		{"staging", "example", "example-staging"},
		{"production", "example", "example-production"},
	} {
		b, err := BucketByKey(tc.key)
		if err != nil {
			t.Fatalf("BucketByKey(%q): %v", tc.key, err)
		}
		if got := b.Name(tc.base); got != tc.want {
			t.Errorf("bucket %q named %q, want %q", tc.key, got, tc.want)
		}
	}
}

// The database bucket keeps the site's configured name with no suffix.
//
// Not cosmetic: renaming it means changing CloudNativePG's destinationPath,
// which starts a fresh WAL archive and needs an immediate base backup. That is
// a live migration nobody should be pushed into by a tidy-up, so the empty
// suffix is load-bearing until a rebuild makes the rename free.
func TestTheDatabaseBucketKeepsTheUnsuffixedName(t *testing.T) {
	b, err := BucketByKey("database")
	if err != nil {
		t.Fatal(err)
	}
	if b.Suffix != "" {
		t.Errorf("the database bucket has suffix %q, want none.\n\n"+
			"Renaming it changes CloudNativePG's destinationPath, which starts a new WAL "+
			"archive with no base backup behind it. If that rename is genuinely wanted it "+
			"belongs in a change that also takes a fresh base backup, not in this table.", b.Suffix)
	}
}

// Every bucket that outlives the estate is marked Keep, and the one that
// describes the estate is not.
//
// Pointed at the direction this actually breaks. Nobody will flip Keep on the
// database bucket - that is a visible, deliberate edit. What happens is a new
// bucket gets added beside the others for some new workload, and whoever adds
// it copies the entry above it without thinking about which one they copied.
// Copy the database entry and the new bucket is destroyed by the next
// teardown, silently, with a zero exit code.
func TestOnlyTheEstatesOwnWorkingDataIsDestroyedWithTheEstate(t *testing.T) {
	kept := map[string]bool{}
	for _, b := range KeptBuckets() {
		kept[b.Key] = true
	}

	if kept["database"] {
		t.Error("the database bucket is marked Keep.\n\n" +
			"It holds the WAL archive and base backups of a database that is about to stop " +
			"existing. Keeping it leaves a bucket nothing tracks, holding backups of nothing.")
	}

	for _, key := range []string{"state", "staging", "production"} {
		if !kept[key] {
			t.Errorf("the %q bucket is not marked Keep.\n\n"+
				"It holds something meant to still be there after a teardown, and a rebuild is "+
				"the routine way a new Talos version reaches these machines. Without Keep, "+
				"Sterilize never takes it out of state and the destroy deletes it - which for "+
				"the state bucket is #94, the backups destroyed by the operation most likely "+
				"to precede needing them.", key)
		}
	}
}

// A key is pasted straight into a resource address, so it has to be a valid
// HCL identifier.
//
// Address() concatenates rather than quoting, because a named resource has no
// quoted key. A key with a space, a dot or a bracket in it would produce an
// address that `tofu state rm` reads as something else entirely - and state rm
// against the wrong address is how a resource stops being tracked without
// anybody deciding that it should.
func TestBucketKeysAreValidResourceNames(t *testing.T) {
	valid := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	for _, b := range Buckets {
		if !valid.MatchString(b.Key) {
			t.Errorf("bucket key %q is not a valid HCL identifier.\n\n"+
				"It is concatenated into a resource address that gets passed to "+
				"`tofu state rm` and `tofu import`. A key that needs escaping is a key "+
				"that will one day be escaped wrongly.", b.Key)
		}
	}
}

func TestBucketKeysAndSuffixesAreUnique(t *testing.T) {
	keys := map[string]bool{}
	suffixes := map[string]bool{}
	for _, b := range Buckets {
		if keys[b.Key] {
			t.Errorf("two buckets share the key %q, so one of them is unreachable "+
				"through BucketByKey and its for_each entry silently replaces the other", b.Key)
		}
		keys[b.Key] = true

		if suffixes[b.Suffix] {
			t.Errorf("two buckets share the suffix %q, so they resolve to the same real "+
				"bucket name - two resources fighting over one bucket", b.Suffix)
		}
		suffixes[b.Suffix] = true
	}
}

func TestEveryBucketSaysWhatItHolds(t *testing.T) {
	for _, b := range Buckets {
		if strings.TrimSpace(b.Holds) == "" {
			t.Errorf("bucket %q does not say what it holds.\n\n"+
				"It is printed in the line that tells an operator a bucket is being kept or "+
				"destroyed, and %q on its own does not tell them whether to worry.", b.Key, b.Key)
		}
	}
}

func TestAddressMatchesTheNamedResource(t *testing.T) {
	b, err := BucketByKey("state")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := b.Address(), "cloudflare_r2_bucket.state"; got != want {
		t.Errorf("Address() = %s, want %s", got, want)
	}
}

func TestBucketByKeyNamesTheKeyItCouldNotFind(t *testing.T) {
	_, err := BucketByKey("worklodas")
	if err == nil {
		t.Fatal("BucketByKey returned no error for a key that is not declared")
	}
	if !strings.Contains(err.Error(), "worklodas") {
		t.Errorf("the error does not name the key that was looked up: %v", err)
	}
}
