package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// released is a scratch repository with a release tagged in it, and a later
// commit the release does not hold.
func released(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.name", "a release")
	run("config", "user.email", "release@example.invalid")
	write(Manifest, `{"holds": ["modules/infrastructure/part", "shared/fact.json"]}`)
	write("modules/infrastructure/part/main.tf", "# as released\n")
	write("shared/fact.json", "{}\n")
	write("elsewhere/kept-out.tf", "# not the release's\n")
	run("add", ".")
	run("commit", "-qm", "the release")
	run("tag", "v2026.1.1")
	write("modules/infrastructure/part/main.tf", "# changed since\n")
	run("commit", "-qam", "since")
	return dir
}

func TestAReleaseIsPlacedAsItWasTagged(t *testing.T) {
	objects := released(t)
	revision, err := Revision(objects, Pin{Version: "v2026.1.1"})
	if err != nil {
		t.Fatal(err)
	}
	into := t.TempDir()
	target, err := PlaceReleasedAs(objects, revision, into, ".released")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(target, "modules/infrastructure/part/main.tf"))
	if err != nil || string(body) != "# as released\n" {
		t.Errorf("the release's module was placed as %q (%v), not as it was tagged", body, err)
	}
	if _, err := os.Stat(filepath.Join(target, "shared/fact.json")); err != nil {
		t.Errorf("a file the release holds outside its modules was not placed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "elsewhere/kept-out.tf")); !os.IsNotExist(err) {
		t.Errorf("a file the release does not hold was placed")
	}
}

func TestAReleaseThatIsNotThereIsRefused(t *testing.T) {
	objects := released(t)
	if _, err := Revision(objects, Pin{Version: "v2026.9.9"}); err == nil || !strings.Contains(err.Error(), "is not a tag this checkout holds") {
		t.Errorf("a release with no tag was given a revision: %v", err)
	}
	if _, err := PlaceReleasedAs(objects, "a-revision-that-is-not-there", t.TempDir(), ".released"); err == nil {
		t.Error("a revision that is not there was placed")
	}
}
