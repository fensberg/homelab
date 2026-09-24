package repo

import (
	"gopkg.in/yaml.v3"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The world's backup and restore scripts, run as shipped against a fake
// rclone whose "world:" remote is a local directory.
//
// The layout is the one the production server actually has, read off its
// volume: each world is a directory, worlds_local/<world>/, with Valheim's own
// rotating backups beside it as <world>_backup_auto-<time>/. The first version
// of these scripts, and these tests, assumed loose worlds_local/<world>.db files
// - the tests passed, and production backed up nothing.
//
// The restore's three outcomes are the property that matters: restore the
// newest backup when the world is missing, let the server start a new world
// only when there is no backup at all, and refuse to start when the bucket
// cannot be read. The last is the one a careless version gets wrong - an
// outage that reads as "no backups" starts a fresh world beside the real one,
// and the next backup begins rotating the real one out.

const fakeRclone = `#!/bin/sh
# A stand-in for rclone: the "world:" remote is the directory $FAKE_BUCKET.
set -eu
cmd="$1"; shift
[ "${FAKE_FAIL:-}" = "$cmd" ] && { echo "fake rclone: $cmd failed" >&2; exit 1; }
case "$cmd" in
  obscure) echo "obscured" ;;
  lsf)
    [ -d "$FAKE_BUCKET" ] || exit 0
    for d in "$FAKE_BUCKET"/*/; do [ -d "$d" ] && basename "$d" | sed 's#$#/#'; done
    ;;
  purge) rm -rf "$FAKE_BUCKET/${1#world:}" ;;
  copy)
    includes=""; src=""; dst=""
    while [ $# -gt 0 ]; do
      case "$1" in
        --include) includes="$includes $2"; shift 2 ;;
        *) if [ -z "$src" ]; then src="$1"; else dst="$1"; fi; shift ;;
      esac
    done
    case "$src" in world:*) src="$FAKE_BUCKET/${src#world:}" ;; esac
    case "$dst" in world:*) dst="$FAKE_BUCKET/${dst#world:}" ;; esac
    mkdir -p "$dst"
    if [ -n "$includes" ]; then
      for f in $includes; do [ -f "$src/$f" ] && cp "$src/$f" "$dst/"; done
    else
      [ "${FAKE_EMPTY_COPY:-}" = "1" ] || cp -R "$src"/. "$dst"/
    fi
    ;;
  *) echo "fake rclone: unexpected $cmd" >&2; exit 64 ;;
esac
`

type worldFixture struct {
	bucket, worlds string
	env            []string
}

func newWorldFixture(t *testing.T) worldFixture {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "rclone"), []byte(fakeRclone), 0o755); err != nil {
		t.Fatal(err)
	}
	f := worldFixture{bucket: filepath.Join(dir, "bucket"), worlds: filepath.Join(dir, "worlds")}
	f.env = []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"FAKE_BUCKET=" + f.bucket, "WORLD_DIR=" + f.worlds,
		"VALHEIM_WORLD_NAME=example", "WORLD_BACKUP_BUCKET=site0-production", "WORLD_BACKUP_KEY=k",
	}
	return f
}

func (f worldFixture) put(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f worldFixture) run(t *testing.T, script string, extra ...string) (bool, string) {
	t.Helper()
	var cmd *exec.Cmd
	switch script {
	case "world-restore.sh":
		cmd = exec.Command("sh", "modules/applications/valheim/image/world-restore.sh")
	case "world-backup.sh":
		cmd = exec.Command("sh", "modules/applications/valheim/image/world-backup.sh")
	default:
		t.Fatalf("no such script %s", script)
	}
	cmd.Dir = repoRoot(t)
	cmd.Env = append(append([]string{}, f.env...), extra...)
	out, err := cmd.CombinedOutput()
	return err == nil, string(out)
}

func (f worldFixture) backups(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(f.bucket)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func TestTheWorldIsRestoredFromTheNewestBackup(t *testing.T) {
	f := newWorldFixture(t)
	f.put(t, filepath.Join(f.bucket, "20260901T000000Z", "example.db"), "old")
	f.put(t, filepath.Join(f.bucket, "20260923T120000Z", "example.db"), "newest")
	f.put(t, filepath.Join(f.bucket, "20260923T120000Z", "example.fwl"), "meta")
	// Valheim's own rotating backups sit beside the world. They are not it.
	f.put(t, filepath.Join(f.worlds, "example_backup_auto-20260924-223711", "example.db"), "valheim's own")

	ok, out := f.run(t, "world-restore.sh")
	if !ok {
		t.Fatalf("the restore failed:\n%s", out)
	}
	got, err := os.ReadFile(filepath.Join(f.worlds, "example", "example.db"))
	if err != nil || string(got) != "newest" {
		t.Errorf("want the newest backup restored, got %q (%v)\n%s", got, err, out)
	}
}

func TestAWorldAlreadyOnTheVolumeIsLeftAlone(t *testing.T) {
	f := newWorldFixture(t)
	f.put(t, filepath.Join(f.worlds, "example", "example.db"), "live")
	f.put(t, filepath.Join(f.bucket, "20260923T120000Z", "example.db"), "backup")

	ok, out := f.run(t, "world-restore.sh")
	got, _ := os.ReadFile(filepath.Join(f.worlds, "example", "example.db"))
	if !ok || string(got) != "live" {
		t.Errorf("the restore replaced a world that was already on the volume (now %q):\n%s", got, out)
	}
}

func TestANewWorldStartsOnlyWhenThereIsNoBackup(t *testing.T) {
	f := newWorldFixture(t)
	ok, out := f.run(t, "world-restore.sh")
	if !ok {
		t.Errorf("with no backup at all the server should be let start a new world, but the restore failed:\n%s", out)
	}
}

func TestTheServerDoesNotStartWhenTheBackupsCannotBeRead(t *testing.T) {
	f := newWorldFixture(t)
	f.put(t, filepath.Join(f.bucket, "20260923T120000Z", "example.db"), "backup")

	ok, out := f.run(t, "world-restore.sh", "FAKE_FAIL=lsf")
	if ok {
		t.Errorf("the restore let the server start although the bucket could not be read.\n\n"+
			"That starts a new world beside a backup that exists, and the first backup "+
			"after it begins rotating the real world out.\n%s", out)
	}
	if !strings.Contains(out, "refusing to start a new world") {
		t.Errorf("the restore failed, but not by refusing for an unreadable bucket:\n%s", out)
	}
}

func TestARestoreThatProducesNoWorldStopsThePod(t *testing.T) {
	f := newWorldFixture(t)
	f.put(t, filepath.Join(f.bucket, "20260923T120000Z", "example.db"), "backup")

	ok, out := f.run(t, "world-restore.sh", "FAKE_EMPTY_COPY=1")
	if ok || !strings.Contains(out, "refusing to start") {
		t.Errorf("a restore that copied nothing let the server start on an empty world:\n%s", out)
	}
}

func TestTheBackupKeepsTheNewestAndRemovesTheRest(t *testing.T) {
	f := newWorldFixture(t)
	f.put(t, filepath.Join(f.worlds, "example", "example.db"), "world")
	f.put(t, filepath.Join(f.worlds, "example", "example.fwl"), "meta")
	f.put(t, filepath.Join(f.worlds, "other", "other.db"), "not this world")
	f.put(t, filepath.Join(f.worlds, "example_backup_auto-20260924-223711", "example.db"), "valheim's own")
	for _, old := range []string{"20260101T000000Z", "20260102T000000Z", "20260103T000000Z"} {
		f.put(t, filepath.Join(f.bucket, old, "example.db"), "old")
	}

	ok, out := f.run(t, "world-backup.sh", "WORLD_BACKUP_ONCE=1", "WORLD_BACKUP_KEEP=2")
	if !ok {
		t.Fatalf("the backup failed:\n%s", out)
	}
	got := f.backups(t)
	if len(got) != 2 || got[0] != "20260103T000000Z" {
		t.Fatalf("want the newest two kept - 20260103T000000Z and the one just taken - got %v\n%s", got, out)
	}
	newest := filepath.Join(f.bucket, got[1])
	for _, want := range []string{"example.db", "example.fwl"} {
		if _, err := os.Stat(filepath.Join(newest, want)); err != nil {
			t.Errorf("the backup just taken is missing %s: %v", want, err)
		}
	}
	for _, stray := range []string{"other.db", "other", "example_backup_auto-20260924-223711"} {
		if _, err := os.Stat(filepath.Join(newest, stray)); err == nil {
			t.Errorf("the backup took %s, which is not this world", stray)
		}
	}
}

func TestAFailedBackupIsLoudRatherThanSilent(t *testing.T) {
	f := newWorldFixture(t)
	f.put(t, filepath.Join(f.worlds, "example", "example.db"), "world")

	ok, out := f.run(t, "world-backup.sh", "WORLD_BACKUP_ONCE=1", "FAKE_FAIL=copy")
	if ok {
		t.Errorf("a backup whose copy failed exited cleanly, so the sidecar would never restart "+
			"and nothing would say backups had stopped:\n%s", out)
	}
}

// The game server's pod runs the restore before the server, backs up beside
// it, and pins all three to one image.
//
// One digest, because expedite rewrites the image lines of this file when
// Valve publishes and the fabricator re-pins the image by name: a container on
// a different digest would be an older image - or one with no rclone at all -
// that nothing moves forward. And the server never mounts the backup Secret:
// the process strangers talk to has no business holding a bucket credential.
func TestTheGameServerIsRestoredBeforeItStartsAndBackedUpBesideIt(t *testing.T) {
	var d struct {
		Spec struct {
			Template struct {
				Spec struct {
					InitContainers []podContainer `yaml:"initContainers"`
					Containers     []podContainer `yaml:"containers"`
				} `yaml:"spec"`
			} `yaml:"template"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(readRepoFile(t, "modules/applications/valheim/base/deployment.yaml")), &d); err != nil {
		t.Fatal(err)
	}
	pod := d.Spec.Template.Spec
	byName := map[string]podContainer{}
	for i, c := range pod.InitContainers {
		c.index = i
		byName[c.Name] = c
	}
	restore, hasRestore := byName["world-restore"]
	backup, hasBackup := byName["world-backup"]
	if !hasRestore || restore.RestartPolicy != "" {
		t.Error("the pod has no world-restore init container that runs to completion before the server, " +
			"so a rebuilt estate starts on an empty world")
	}
	if !hasBackup || backup.RestartPolicy != "Always" {
		t.Error("the pod has no world-backup sidecar (an init container with restartPolicy: Always), " +
			"so nothing backs the world up")
	}
	if hasRestore && hasBackup && backup.index < restore.index {
		t.Error("world-backup starts before world-restore, so it could back up the empty world a " +
			"restore is about to replace")
	}

	var images []string
	for _, c := range append(append([]podContainer{}, pod.InitContainers...), pod.Containers...) {
		images = append(images, c.Image)
		if c.Name == "world-restore" || c.Name == "world-backup" {
			continue
		}
		for _, e := range c.EnvFrom {
			if e.SecretRef.Name == "valheim-backup" {
				t.Errorf("container %s mounts the valheim-backup Secret; only the restore and backup may hold the bucket credential", c.Name)
			}
		}
	}
	for _, img := range images[1:] {
		if img != images[0] {
			t.Errorf("the pod's containers run different images (%v); one digest must pin the server, its restore and its backup", images)
			break
		}
	}
}

type podContainer struct {
	Name          string `yaml:"name"`
	Image         string `yaml:"image"`
	RestartPolicy string `yaml:"restartPolicy"`
	EnvFrom       []struct {
		SecretRef struct {
			Name string `yaml:"name"`
		} `yaml:"secretRef"`
	} `yaml:"envFrom"`
	index int
}
