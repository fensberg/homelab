package config

import (
	"regexp"
	"strings"
	"testing"
)

func TestEveryBucketIsTheSiteSlugPlusASuffix(t *testing.T) {
	net := &SiteNetwork{Name: "example"}
	for _, tc := range []struct {
		key  string
		want string
	}{
		{"database", "example-database"},
		{"state", "example-state"},
		{"staging", "example-staging"},
		{"production", "example-production"},
	} {
		b, err := BucketByKey(tc.key)
		if err != nil {
			t.Fatalf("BucketByKey(%q): %v", tc.key, err)
		}
		if got := b.Name(net); got != tc.want {
			t.Errorf("bucket %q named %q, want %q", tc.key, got, tc.want)
		}
	}
}

// No bucket takes its name from anywhere but the site slug.
//
// The database bucket used to, which forced Name to take a second argument
// that three of four callers passed and ignored - and an argument most callers
// ignore is one a caller eventually passes wrongly. The operator retired that
// by renaming the bucket to carry a suffix like the others.
func TestNoBucketEscapesTheSiteSlug(t *testing.T) {
	net := &SiteNetwork{Name: "example"}
	for _, b := range Buckets {
		if b.Suffix == "" {
			t.Errorf("bucket %q has no suffix, so it resolves to the bare site slug.\n\n"+
				"Every bucket is the slug plus a suffix. An empty one collides with the "+
				"site's own name and reintroduces the special case that was just removed.", b.Key)
		}
		if got := b.Name(net); got == net.Name {
			t.Errorf("bucket %q resolves to the bare site name %q", b.Key, got)
		}
	}
}

// A credential is declared for exactly the buckets something writes to.
//
// Staging and production deliberately have none: nothing writes to them yet,
// and a credential issued ahead of a writer is a live key nobody is watching.
// CredentialFor must say so rather than hand back a zero value, because an
// empty key pair fails much later inside rclone as "credentials are empty",
// naming no bucket.
func TestOnlyTheBucketsWithWritersHaveCredentials(t *testing.T) {
	store := ObjectStorage{
		Database: ObjectStorageCredential{AccessKeyID: "db", SecretAccessKey: "db-secret"},
		State:    ObjectStorageCredential{AccessKeyID: "st", SecretAccessKey: "st-secret"},
	}

	for _, key := range []string{"database", "state"} {
		cred, err := store.CredentialFor(key)
		if err != nil {
			t.Errorf("no credential for %q, which has a writer: %v", key, err)
			continue
		}
		if cred.AccessKeyID == "" || cred.SecretAccessKey == "" {
			t.Errorf("the %q credential is half empty: %+v", key, cred)
		}
	}

	for _, key := range []string{"staging", "production"} {
		if _, err := store.CredentialFor(key); err == nil {
			t.Errorf("CredentialFor(%q) returned no error.\n\n"+
				"Nothing writes to that bucket yet, so there is no credential. Returning a "+
				"zero value instead of an error moves the failure into rclone, where it "+
				"reads as \"credentials are empty\" and names no bucket.", key)
		}
	}
}

// The two credentials must not be the same key pair.
//
// The whole point of the split is that the key living permanently in a cluster
// Secret cannot delete the state dumps. Pasting one token into both vault
// fields restores exactly the situation the split removed, and nothing about
// the config would look wrong - the fields are adjacent and near-identically
// named.
func TestConfigRefusesOneKeyInBothFields(t *testing.T) {
	site := validSite()
	site.ObjectStorage.State.AccessKeyID = site.ObjectStorage.Database.AccessKeyID
	cfg := &Config{Tunnel: validTunnel(), Sites: map[string]Site{"site0": site}}

	_, err := ResolveSiteNetwork(cfg, "site0")
	if err == nil {
		t.Fatal("a config carrying one access_key_id in both the database and state fields " +
			"was accepted.\n\n" +
			"That is one credential reaching both buckets, which is the situation the split " +
			"exists to remove: the key that sits permanently in a cluster Secret can then " +
			"delete the state dumps that exist to survive that cluster being lost.")
	}
	if !strings.Contains(err.Error(), "same access_key_id") {
		t.Errorf("refused, but not for the shared key: %v", err)
	}
}

// Two different keys are accepted, so the check above is not refusing
// everything. Without this, the assertion would still pass if the config had
// simply become unloadable for some unrelated reason.
func TestTwoDistinctCredentialsAreAccepted(t *testing.T) {
	site := validSite()
	if site.ObjectStorage.Database.AccessKeyID == site.ObjectStorage.State.AccessKeyID {
		t.Fatal("validSite() carries the same key in both fields, so this proves nothing")
	}
	cfg := &Config{Tunnel: validTunnel(), Sites: map[string]Site{"site0": site}}
	if _, err := ResolveSiteNetwork(cfg, "site0"); err != nil {
		t.Errorf("two distinct credentials were refused: %v", err)
	}
}

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
