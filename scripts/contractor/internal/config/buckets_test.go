package config

import (
	"regexp"
	"strings"
	"testing"
)

func TestBucketNameIsTheSiteSlugPlusTheSuffix(t *testing.T) {
	net := &SiteNetwork{Name: "example"}
	for _, tc := range []struct {
		key  string
		want string
	}{
		// The database bucket is named from the config, not the slug - see
		// TestOnlyTheDatabaseBucketTakesItsNameFromTheConfig.
		{"database", "configured"},
		{"state", "example-state"},
		{"staging", "example-staging"},
		{"production", "example-production"},
	} {
		b, err := BucketByKey(tc.key)
		if err != nil {
			t.Fatalf("BucketByKey(%q): %v", tc.key, err)
		}
		if got := b.Name(net, "configured"); got != tc.want {
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

// Two sites cannot be given names that collapse to one slug.
//
// The slug names a site's VMs and its buckets. Two sites are two Proxmox
// clusters, so duplicate VM names never met - but every site in an estate
// shares ONE object storage account, so duplicate slugs mean two sites writing
// into each other's state dumps with nothing saying so.
//
// Asserted on the slug rather than the raw name, which is the whole point:
// "North Street Office" and "north-street-office " are two names and one bucket.
func TestTwoSitesCannotCollapseToOneSlug(t *testing.T) {
	// Each pair collapses by a different mechanism: trailing whitespace, a
	// different separator, and case. All three are things somebody types into a
	// vault field without thinking of them as the same value.
	for _, tc := range []struct{ a, b string }{
		{"north-street-office", "north-street-office "},
		{"north street office", "north_street_office"},
		{"north-street-office", "North-Street-Office"},
	} {
		if got, want := SiteSlug(tc.a, "site0"), SiteSlug(tc.b, "site1"); got != want {
			t.Errorf("SiteSlug(%q) = %q and SiteSlug(%q) = %q; this case is only "+
				"interesting if they collide, so the test needs a different pair",
				tc.a, got, tc.b, want)
			continue
		}

		cfg := &Config{Sites: map[string]Site{
			"site0": {Name: tc.a, Octet: 10, ControlPlaneCount: 1},
			"site1": {Name: tc.b, Octet: 20, ControlPlaneCount: 1},
		}}
		_, err := ResolveSiteNetwork(cfg, "site0")
		if err == nil {
			t.Errorf("two sites named %q and %q were accepted; they share the slug %q, "+
				"so they share every bucket", tc.a, tc.b, SiteSlug(tc.a, "site0"))
			continue
		}
		if !strings.Contains(err.Error(), "slug") {
			t.Errorf("names %q and %q were refused, but not for the slug collision: %v",
				tc.a, tc.b, err)
		}
	}
}

// A site with no name falls back to its key, which is unique by construction.
func TestAnUnnamedSiteFallsBackToItsKey(t *testing.T) {
	if got := SiteSlug("", "site7"); got != "site7" {
		t.Errorf("SiteSlug(\"\", \"site7\") = %q, want \"site7\"", got)
	}
}

// The database bucket is the one exception, and it is temporary.
func TestOnlyTheDatabaseBucketTakesItsNameFromTheConfig(t *testing.T) {
	net := &SiteNetwork{Name: "north-street-office"}

	for _, b := range Buckets {
		got := b.Name(net, "legacy-name")
		if b.Key == "database" {
			if got != "legacy-name" {
				t.Errorf("the database bucket resolved to %q, want the configured name.\n\n"+
					"It cannot move to the site slug in place: Cloudflare refuses to delete a "+
					"bucket holding objects, and this one holds the WAL archive continuously. "+
					"The rename happens at the next rebuild.", got)
			}
			continue
		}
		if want := "north-street-office" + b.Suffix; got != want {
			t.Errorf("bucket %q resolved to %q, want %q - it must be named for the site, "+
				"because the site is the isolation boundary and one R2 account holds them all",
				b.Key, got, want)
		}
	}
}
