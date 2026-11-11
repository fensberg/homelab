package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"homelab/tests/parsed"
)

// rollHistory is a repository whose machines were moved from one Talos
// version to another, and whose versions file then changed for some other
// tool: the bump is the commit that moved the version, not the last one to
// touch the file.
func rollHistory(t *testing.T, before, after, upgradesFrom string) (dir, bump string) {
	t.Helper()
	dir = t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "commit.gpgsign", "false")
	git("config", "user.name", "whoever bumped it")
	git("config", "user.email", "bump@example.invalid")
	write("modules/infrastructure/image/declares.tf", "locals {\n  talos_upgrades_from = \""+upgradesFrom+"\"\n}\n")
	write("scripts/versions.env", "TALOS_VERSION="+before+"\nTASK_VERSION=3.53.1\n")
	git("add", ".")
	git("commit", "-qm", "where the machines were")
	write("scripts/versions.env", "TALOS_VERSION="+after+"\nTASK_VERSION=3.53.1\n")
	git("commit", "-qam", "the bump")
	bump = git("rev-parse", "HEAD")
	write("scripts/versions.env", "TALOS_VERSION="+after+"\nTASK_VERSION=3.54.0\n")
	git("commit", "-qam", "another tool moves")
	return dir, bump
}

func rollRevert(t *testing.T, dir, touched string) (string, error) {
	t.Helper()
	out, errs, err := repository(t).Invoke(parsed.Run{In: dir, Env: []string{"TOUCHED=" + touched}}, "bash", parsed.Here("scripts/roll-revert.sh"))
	return strings.TrimSpace(string(out)) + string(errs), err
}

func TestAStoppedRollOffersToPutBackOnlyItsVersion(t *testing.T) {
	light(t)
	for _, c := range []struct {
		name, before, after, from, touched string
		want                               []string
	}{
		{"a patch bump that moved a machine", "v1.13.9", "v1.13.11", "v1.13", "true",
			[]string{"version=v1.13.11", "previous=v1.13.9", "revertible=true"}},
		{"a patch bump that moved nothing", "v1.13.9", "v1.13.11", "v1.13", "false",
			[]string{"previous=v1.13.9", "revertible=false"}},
		{"a bump across a minor version", "v1.13.11", "v1.14.0", "v1.14", "true",
			[]string{"version=v1.14.0", "previous=v1.13.11", "revertible=false"}},
	} {
		dir, bump := rollHistory(t, c.before, c.after, c.from)
		out, err := rollRevert(t, dir, c.touched)
		if err != nil {
			t.Errorf("%s: %v\n%s", c.name, err, out)
			continue
		}
		for _, w := range append(c.want, "sha="+bump) {
			if !strings.Contains(out, w) {
				t.Errorf("%s: said %q, want %q", c.name, out, w)
			}
		}
	}
}

// A version that cannot be read is not a version to offer: an empty one is
// what made every stopped roll offer nothing, and say so to nobody (#853).
func TestARollWhoseVersionCannotBeReadIsNotAnswered(t *testing.T) {
	light(t)
	dir, _ := rollHistory(t, "v1.13.9", "v1.13.11", "v1.13")
	if err := os.WriteFile(filepath.Join(dir, "scripts", "versions.env"), []byte("TASK_VERSION=3.54.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := rollRevert(t, dir, "true"); err == nil {
		t.Errorf("a versions file with no Talos version was answered: %s", out)
	}
}
