package config

import (
	"regexp"
	"strings"
	"testing"
)

// Every bucket the site declares has a credential of its own.
//
// Walked from Buckets rather than listed here, so a bucket added to the set
// without a credential beside it fails this rather than reaching rclone with an
// empty key - which fails much later, as "credentials are empty", naming no
// bucket. A key nobody declared is refused rather than answered with a zero
// value, for the same reason.
func TestEveryBucketHasACredentialOfItsOwn(t *testing.T) {
	site := validSite()
	ids := map[string]string{}
	for _, b := range Buckets {
		cred, err := site.ObjectStorage.CredentialFor(b.Key)
		if err != nil {
			t.Errorf("bucket %q has no credential field: %v", b.Key, err)
			continue
		}
		if cred.AccessKeyID == "" || cred.SecretAccessKey == "" {
			t.Errorf("validSite gives the %q bucket a half-empty credential, so this proves nothing about it", b.Key)
		}
		if other, dup := ids[cred.AccessKeyID]; dup {
			t.Errorf("validSite gives %q and %q the same key, so the shared-key refusal cannot be tested against it", other, b.Key)
		}
		ids[cred.AccessKeyID] = b.Key
	}
	if _, err := site.ObjectStorage.CredentialFor("nonexistent"); err == nil {
		t.Error("CredentialFor answered for a bucket nobody declared")
	}
}

// The two credentials must not be the same key pair.
//
// The whole point of the split is that the key living permanently in a cluster
// Secret cannot delete the state dumps. Pasting one token into both vault
// fields restores exactly the situation the split removed, and nothing about
// the config would look wrong - the fields are adjacent and near-identically
// named.
func TestConfigRefusesOneKeyInTwoFields(t *testing.T) {
	// The pairs that matter most, and one that does not involve the estate's
	// own recovery material - so the check is shown to cover every pair
	// rather than the two fields it was first written for.
	for _, pair := range [][2]string{
		{"database", "state"},
		{"state", "production"},
		{"staging", "production"},
	} {
		site := validSite()
		src, _ := site.ObjectStorage.CredentialFor(pair[0])
		switch pair[1] {
		case "state":
			site.ObjectStorage.State.AccessKeyID = src.AccessKeyID
		case "staging":
			site.ObjectStorage.Staging.AccessKeyID = src.AccessKeyID
		case "production":
			site.ObjectStorage.Production.AccessKeyID = src.AccessKeyID
		}
		cfg := &Config{Tunnel: validTunnel(), Sites: map[string]Site{"site0": site}}

		_, err := ResolveSiteNetwork(cfg, "site0")
		if err == nil {
			t.Errorf("one access_key_id in both %s and %s was accepted.\n\n"+
				"Each credential is scoped to one bucket; a key in two fields lets the holder "+
				"of either reach both - for database and state, the key in a cluster Secret "+
				"could then delete the dumps that exist to survive that cluster.", pair[0], pair[1])
			continue
		}
		if !strings.Contains(err.Error(), "same access_key_id") {
			t.Errorf("%s/%s refused, but not for the shared key: %v", pair[0], pair[1], err)
		}
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

func TestBucketKeysAreUnique(t *testing.T) {
	keys := map[string]bool{}
	for _, b := range Buckets {
		if keys[b.Key] {
			t.Errorf("two buckets share the key %q, so one of them is unreachable "+
				"through BucketByKey and the estate's for_each entry silently replaces the other", b.Key)
		}
		keys[b.Key] = true
	}
}

// A site teardown empties the database bucket and nothing else. The database's
// WAL archive describes a cluster that is ending, and CloudNativePG refuses to
// archive a new one into it; everything else is meant to still be there
// after the teardown, and emptying the state bucket is #94 - the backups
// destroyed by the operation most likely to precede needing them.
func TestATeardownEmptiesTheDatabaseBucketAndNothingElse(t *testing.T) {
	for _, b := range Buckets {
		if want := b.Key == "database"; b.EmptiedAtTeardown != want {
			t.Errorf("the %q bucket has EmptiedAtTeardown=%v; only the database bucket is emptied by a site teardown", b.Key, b.EmptiedAtTeardown)
		}
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

// The paths are written against the remote RcloneEnv defines, so a rename of
// one cannot leave the other pointing at a remote that does not exist.
func TestBackupPathsUseTheRemoteRcloneIsGiven(t *testing.T) {
	env := strings.Join(RcloneEnv("acct", ObjectStorageCredential{}), "\n")
	remote := strings.SplitN(BucketRemote("b"), ":", 2)[0]
	if !strings.Contains(env, "RCLONE_CONFIG_"+remote+"_TYPE=") {
		t.Errorf("paths address the remote %q, but RcloneEnv configures a different one:\n%s", remote, env)
	}
	if got, want := LatestStateBackupPath("b"), BucketRemote("b")+"/"+StateBackupFolder+"/"+LatestStateBackup; got != want {
		t.Errorf("LatestStateBackupPath = %q, want %q", got, want)
	}
}

// Slug is the transform alone; SiteSlug adds the site's fallback. The split
// exists so the forkability check can slug an organization's name without
// inheriting a fallback that only means something for a site.
func TestSlugHasNoFallbackAndSiteSlugDoes(t *testing.T) {
	if got := Slug("North Street Office"); got != "north-street-office" {
		t.Errorf("Slug = %q, want north-street-office", got)
	}
	if got := Slug("   "); got != "" {
		t.Errorf("Slug of nothing = %q, want empty - a fallback here would invent a name", got)
	}
	if got := SiteSlug("   ", "site3"); got != "site3" {
		t.Errorf("SiteSlug with no usable name = %q, want the key", got)
	}
}

// The state backups are reached with the state credential, in the bucket the
// estate granted for them - and an empty half of the credential, or no bucket
// name, is refused, which Backup and Restore did not agree about while each
// had its own copy.
func TestStateBackupLocationUsesTheStateBucketAndCredential(t *testing.T) {
	site := validSite()
	cfg := &Config{Tunnel: validTunnel(), Sites: map[string]Site{"site0": site}}

	loc, err := StateBackupLocation(cfg, "site0")
	if err != nil {
		t.Fatal(err)
	}
	if want := StateBackupPath(site.ObjectStorage.State.Bucket); loc.Folder != want {
		t.Errorf("Folder = %q, want %q", loc.Folder, want)
	}
	env := strings.Join(loc.Env, "\n")
	if !strings.Contains(env, site.ObjectStorage.State.AccessKeyID) {
		t.Error("the environment does not carry the state credential")
	}
	if strings.Contains(env, site.ObjectStorage.Database.AccessKeyID) {
		t.Error("the environment carries the database credential - the key that lives in the cluster must not reach the state dumps")
	}

	for _, half := range []string{"key id", "secret", "bucket name"} {
		broken := validSite()
		switch half {
		case "key id":
			broken.ObjectStorage.State.AccessKeyID = ""
		case "secret":
			broken.ObjectStorage.State.SecretAccessKey = ""
		default:
			broken.ObjectStorage.State.Bucket = ""
		}
		cfg := &Config{Tunnel: validTunnel(), Sites: map[string]Site{"site0": broken}}
		if _, err := StateBackupLocation(cfg, "site0"); err == nil {
			t.Errorf("an empty state %s was accepted", half)
		}
	}
}

// The path every existing backup is stored at, written out rather than rebuilt
// from the constants that produce it.
//
// This test used to live in restore_test.go against backupObjectKey, and when
// that wrapper went its replacement compared LatestStateBackupPath with a
// concatenation of the same constants - which passes whatever the constants
// say. A rename of the folder or the pointer object would have gone through
// green, and every backup already in the bucket would then sit where Restore
// no longer looks. A restore that finds nothing reports that there is no
// backup, at the moment somebody is recovering an estate.
func TestTheStateBackupPathIsWhereExistingBackupsAre(t *testing.T) {
	if got, want := LatestStateBackupPath("my-bucket"), "R2:my-bucket/management-cluster/latest.tfstate.age"; got != want {
		t.Errorf("LatestStateBackupPath = %q, want %q.\n\n"+
			"Every backup already stored is at the second path. Changing where new ones go "+
			"strands the old ones where Restore no longer looks - move them in the same change, "+
			"or keep the path.", got, want)
	}
}
