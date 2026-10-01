package phases

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/contractor/config"
	"homelab/contractor/internal/run"
)

// fakeTools puts a tofu, an age and an rclone on PATH that record what they
// were asked and answer from files in the directory returned: `state` is what
// `tofu state pull` and `age -d` print, `listing` what `rclone lsjson` prints,
// and `cat` what `rclone cat` prints.
func fakeTools(t *testing.T, answers map[string]string) (dir string, calls func() string) {
	t.Helper()
	dir = t.TempDir()
	for name, body := range answers {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(dir, "calls")
	scripts := map[string]string{
		"tofu": "cat " + filepath.Join(dir, "state") + "\n",
		// Encrypting writes its --output file; decrypting prints the state.
		"age":    "case \"$1\" in\n  -d) cat " + filepath.Join(dir, "state") + " ;;\n  *) cat > \"$4\" ;;\nesac\n",
		"rclone": "case \"$3\" in\n  lsjson) cat " + filepath.Join(dir, "listing") + " ;;\n  cat) cat " + filepath.Join(dir, "cat") + " ;;\nesac\n",
	}
	for name, body := range scripts {
		script := "#!/bin/sh\necho \"" + name + " $*\" >> " + log + "\n" + body
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir, func() string {
		b, _ := os.ReadFile(log)
		return string(b)
	}
}

// backupsOf is where a root's backups go, resolved the way Backup and Restore
// resolve it.
func backupsOf(t *testing.T, root string) config.StateBackups {
	t.Helper()
	cfg := &config.Config{Sites: map[string]config.Site{"site0": {ObjectStorage: config.ObjectStorage{
		AccountID: "account",
		State:     config.ObjectStorageCredential{Bucket: "bucket", AccessKeyID: "key", SecretAccessKey: "secret"},
	}}}}
	loc, err := config.StateBackupLocation(cfg, "site0", root)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

const realisticState = `{"version": 4, "serial": 7, "lineage": "0f0f0f0f-aaaa-bbbb-cccc-121212121212",
  "resources": [{"mode": "managed", "type": "kubernetes_namespace", "name": "database", "instances": []}]}`

// One root's state is encrypted and uploaded to that root's own folder, as a
// timestamped generation and as the pointer a restore reads first.
func TestARootsStateIsBackedUpToItsOwnFolder(t *testing.T) {
	dir, calls := fakeTools(t, map[string]string{
		"state":   realisticState,
		"listing": `[{"Path": "20261001-120000.tfstate.age"}, {"Path": "latest.tfstate.age"}]`,
	})
	platform := run.Root{Name: config.PlatformRoot, Dir: dir}
	ctx := (&run.Context{Site: "site0"}).In(platform)
	loc := backupsOf(t, config.PlatformRoot)
	if err := backupRoot(ctx, loc, "age1recipient", "20261001-120000"); err != nil {
		t.Fatal(err)
	}
	got := calls()
	for _, want := range []string{
		"tofu state pull",
		"age --recipient age1recipient --output",
		"R2:bucket/management-platform/20261001-120000.tfstate.age",
		"R2:bucket/management-platform/latest.tfstate.age",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("nothing ran %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "management-cluster") {
		t.Errorf("the platform root's state went to the cluster root's folder:\n%s", got)
	}
}

// A state too small to be one is refused before anything is uploaded: an
// empty pull overwriting the pointer would replace the last good backup.
func TestAStateTooSmallToBeOneIsNotUploaded(t *testing.T) {
	dir, calls := fakeTools(t, map[string]string{"state": `{}`, "listing": `[]`})
	ctx := (&run.Context{Site: "site0"}).In(run.Root{Name: config.ClusterRoot, Dir: dir})
	err := backupRoot(ctx, backupsOf(t, config.ClusterRoot), "age1recipient", "20261001-120000")
	if err == nil || !strings.Contains(err.Error(), "refusing to upload") {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(calls(), "rclone") {
		t.Errorf("something was uploaded:\n%s", calls())
	}
}

// A root's latest backup comes back as state and says what it describes; an
// empty object, and one that is not state, are refused.
func TestARootsBackupIsFetchedDecryptedAndChecked(t *testing.T) {
	dir, calls := fakeTools(t, map[string]string{"state": realisticState, "cat": "age-encryption.org/v1 ciphertext"})
	ctx := (&run.Context{Site: "site0"}).In(run.Root{Name: config.ClusterRoot, Dir: dir})
	loc := backupsOf(t, config.ClusterRoot)
	identity := []byte("AGE-SECRET-KEY-1TEST")

	plain, summary, err := fetchBackup(ctx, loc, identity)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != realisticState || summary.Serial != 7 || summary.Resources != 1 {
		t.Errorf("got %+v and %d bytes", summary, len(plain))
	}
	if !strings.Contains(calls(), "cat R2:bucket/management-cluster/latest.tfstate.age") {
		t.Errorf("the pointer object was not what was fetched:\n%s", calls())
	}

	if err := os.WriteFile(filepath.Join(dir, "cat"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fetchBackup(ctx, loc, identity); err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Errorf("an empty backup was restored: %v", err)
	}

	for name, body := range map[string]string{"cat": "ciphertext", "state": `{"version": 4, "lineage": "l", "resources": []}`} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := fetchBackup(ctx, loc, identity); err == nil || !strings.Contains(err.Error(), "no resources") {
		t.Errorf("a backup describing nothing was restored: %v", err)
	}
}
