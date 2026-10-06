package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homelab/details/applications"
	"homelab/details/cloudflare"
	"homelab/details/holds"
)

// The vault and the storage are stand-ins on PATH: this test binary, under
// the names `op` and `rclone`, answering from files in a directory. What the
// officer runs is then the real command line, and a test says what the vault
// holds and what the bucket lists.
func TestMain(m *testing.M) {
	if dir := os.Getenv(standInDir); dir != "" {
		switch filepath.Base(os.Args[0]) {
		case "op":
			os.Exit(standInVault(dir, os.Args[1:]))
		case "rclone":
			os.Exit(standInStorage(dir, os.Args[1:]))
		}
	}
	os.Exit(m.Run())
}

const standInDir = "STAND_IN_ESTATE"

// standInVault answers `op read op://vault/item/field` from the file named
// for the field, and fails for a field with no file.
func standInVault(dir string, args []string) int {
	if len(args) != 2 || args[0] != "read" {
		return 2
	}
	body, err := os.ReadFile(filepath.Join(dir, "vault", filepath.Base(args[1])))
	if err != nil {
		return 1
	}
	fmt.Println(string(body))
	return 0
}

// standInStorage answers `rclone lsjson ... remote:bucket/folder` with the
// listing kept for that bucket, records what it was asked and with which
// key, and fails for a bucket with no listing.
func standInStorage(dir string, args []string) int {
	target := args[len(args)-1]
	asked := strings.Join(args, " ") + " key=" + os.Getenv(cloudflare.RcloneVar(remote, cloudflare.RcloneKeyID)) + "\n"
	f, _ := os.OpenFile(filepath.Join(dir, "asked"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	f.WriteString(asked)
	f.Close()
	_, path, _ := strings.Cut(target, ":")
	bucket, _, _ := strings.Cut(path, "/")
	body, err := os.ReadFile(filepath.Join(dir, "listing-"+bucket))
	if err != nil {
		return 3
	}
	fmt.Print(string(body))
	return 0
}

var now = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// estate is a repository with one application, a site that runs it, and the
// stand-ins that answer for the site's vault and buckets.
type estate struct {
	t    *testing.T
	root string
	dir  string
}

const world = `{"what": "the world", "lifetime": "client", "lives_on": "machine", "held_by": "PersistentVolumeClaim/world", "copy": {"under": "worlds/"}, "may_lose": "2h"}`

func newEstate(t *testing.T, appHolds, coreHolds string) estate {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	e := estate{t, t.TempDir(), t.TempDir()}
	bin := t.TempDir()
	for _, name := range []string{"op", "rclone"} {
		if err := os.Symlink(self, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(standInDir, e.dir)

	e.write(filepath.Join(e.root, applications.Dir, "game", applications.Declaration),
		`{"requires": [], "holds": [`+appHolds+`]}`)
	e.write(filepath.Join(e.root, applications.SiteFilePath("site0")),
		"spec:\n  path: ./"+applications.Dir+"/game/production\n")
	if coreHolds != "" {
		e.write(filepath.Join(e.root, holds.CoreFile), `{"holds": [`+coreHolds+`]}`)
	}
	for field, value := range map[string]string{
		"account_id":                          "account",
		"production_bucket":                   "bucket-p",
		"production_reader_access_key_id":     "reader-p",
		"production_reader_secret_access_key": "secret-p",
	} {
		e.write(filepath.Join(e.dir, "vault", field), value)
	}
	return e
}

func (e estate) write(path, body string) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

// lists says what the production bucket holds: one object for each age.
func (e estate) lists(ages ...time.Duration) {
	var objects []string
	for i, age := range ages {
		objects = append(objects, fmt.Sprintf(`{"Path": "worlds/w/%d/file", "ModTime": %q}`, i, now.Add(-age).Format(time.RFC3339)))
	}
	e.write(filepath.Join(e.dir, "listing-bucket-p"), "["+strings.Join(objects, ",")+"]")
}

func (e estate) clear() (bool, string) {
	var out strings.Builder
	ok := clear(&out, e.root, "site0", "site", now)
	return ok, out.String()
}

func (e estate) asked() string {
	b, _ := os.ReadFile(filepath.Join(e.dir, "asked"))
	return string(b)
}

func TestASiteWhoseClientDataHasAFreshCopyIsCleared(t *testing.T) {
	e := newEstate(t, world, "")
	e.lists(72*time.Hour, 30*time.Minute)
	ok, out := e.clear()
	if !ok {
		t.Fatalf("a world copied half an hour ago, allowed two hours, was refused:\n%s", out)
	}
	asked := e.asked()
	for _, want := range []string{"lsjson", "--use-server-modtime", remote + ":bucket-p/worlds", "key=reader-p"} {
		if !strings.Contains(asked, want) {
			t.Errorf("the storage was not asked with %q, so this did not look where the copy is, as the storage dates it, with the key that only reads:\n%s", want, asked)
		}
	}
}

func TestACopyOlderThanMayBeLostIsRefusedByName(t *testing.T) {
	e := newEstate(t, world, "")
	e.lists(72 * time.Hour)
	ok, out := e.clear()
	if ok {
		t.Fatalf("a world whose newest copy is three days old, allowed two hours, was cleared:\n%s", out)
	}
	for _, want := range []string{"REFUSED", "the world (game)", "3 days old", "2 hours", "The officer changed nothing"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
	for _, leaked := range []string{"bucket-p", "reader-p", "secret-p", "account"} {
		if strings.Contains(out, leaked) {
			t.Errorf("the refusal carries %q, a value from the vault:\n%s", leaked, out)
		}
	}
}

func TestWhatCannotBeLookedAtIsRefused(t *testing.T) {
	for name, arrange := range map[string]func(estate){
		"a bucket with no copy in it":    func(e estate) { e.lists() },
		"a bucket that cannot be listed": func(e estate) {},
		"a vault without the key that reads it": func(e estate) {
			e.lists(time.Minute)
			os.Remove(filepath.Join(e.dir, "vault", "production_reader_access_key_id"))
		},
		"a listing that is not one": func(e estate) { e.write(filepath.Join(e.dir, "listing-bucket-p"), "not json") },
	} {
		t.Run(name, func(t *testing.T) {
			e := newEstate(t, world, "")
			arrange(e)
			if ok, out := e.clear(); ok || !strings.Contains(out, "the world (game)") {
				t.Fatalf("%s was cleared, or the refusal does not name what would be lost:\n%s", name, out)
			}
		})
	}
}

func TestSomethingThatOutlivesTheSiteWithNoCopyIsRefused(t *testing.T) {
	e := newEstate(t, `{"what": "the ledger", "lifetime": "estate", "lives_on": "site", "held_by": "PersistentVolumeClaim/ledger"}`, "")
	if ok, out := e.clear(); ok || !strings.Contains(out, "none anywhere else") {
		t.Fatalf("something worth keeping for the estate's life, with no copy, was cleared for the site's destruction:\n%s", out)
	}
	if e.asked() != "" {
		t.Error("the storage was asked about an asset that declares no copy")
	}
}

func TestWhatDiesWithTheSiteDoesNotStandInItsWay(t *testing.T) {
	history := `{"what": "the metrics history", "lifetime": "site", "lives_on": "machine", "held_by": "HelmRelease/metrics"}`
	e := newEstate(t, "", history)
	ok, out := e.clear()
	if !ok {
		t.Fatalf("a site's own history, which is worth keeping only while the site lives, stopped the site being destroyed:\n%s", out)
	}
	if e.asked() != "" {
		t.Error("the storage was asked about an asset the destruction does not endanger")
	}
}

func TestTheCoresAssetsAreAskedAboutInTheBucketTheyName(t *testing.T) {
	e := newEstate(t, "", `{"what": "the deeds", "lifetime": "estate", "lives_on": "site", "held_by": "Cluster/deeds", "copy": {"storage": "production", "under": "deeds"}, "may_lose": "1h"}`)
	e.lists(10 * time.Minute)
	if ok, out := e.clear(); !ok {
		t.Fatalf("a core asset with a ten-minute-old copy, allowed an hour, was refused:\n%s", out)
	}
	if !strings.Contains(e.asked(), remote+":bucket-p/deeds") {
		t.Errorf("the core's copy was not looked for where it says it is:\n%s", e.asked())
	}
}

func TestAnApplicationTheSiteDoesNotRunIsNotAskedAbout(t *testing.T) {
	e := newEstate(t, world, "")
	e.write(filepath.Join(e.root, applications.SiteFilePath("site0")), "spec: {}\n")
	if ok, out := e.clear(); !ok || e.asked() != "" {
		t.Fatalf("a site that runs no application was refused over one, or the storage was asked:\n%s", out)
	}
}

func TestWhatHoldsCannotBeReadIsRefused(t *testing.T) {
	e := newEstate(t, `{"what": "the world", "lifetime": "forever", "lives_on": "machine", "held_by": "PersistentVolumeClaim/world"}`, "")
	if ok, out := e.clear(); ok || !strings.Contains(out, "cannot be made out") {
		t.Fatalf("a declaration with a lifetime that is not a scope was cleared:\n%s", out)
	}
}

func TestOnlyASitesDestructionCanBeClearedYet(t *testing.T) {
	e := newEstate(t, world, "")
	var out strings.Builder
	if clear(&out, e.root, "site0", "machine", now) {
		t.Fatalf("destroying a machine was cleared, and nothing here judges that yet:\n%s", out.String())
	}
}
