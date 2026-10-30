package gitenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsolatePointsBothConfigsAtNothing(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/home/someone/.gitconfig")
	t.Setenv("GIT_CONFIG_SYSTEM", "/etc/gitconfig")
	Isolate()
	for _, k := range []string{"GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM"} {
		if got := os.Getenv(k); got != os.DevNull {
			t.Errorf("%s is %q, want %q", k, got, os.DevNull)
		}
	}
}

// Every variable git itself says binds it to a repository, so that one a later
// git adds is met here rather than in a hook.
func TestIsolateClearsEverythingThatBindsGitToARepository(t *testing.T) {
	out, err := exec.Command("git", "rev-parse", "--local-env-vars").Output()
	if err != nil {
		t.Fatal(err)
	}
	names := strings.Fields(string(out))
	if len(names) == 0 {
		t.Fatal("git lists no variables, so nothing was checked")
	}
	for _, name := range names {
		t.Setenv(name, "from-a-hook")
	}
	Isolate()
	for _, name := range names {
		if got, set := os.LookupEnv(name); set {
			t.Errorf("%s is still %q, so a fixture's git can reach the repository a hook runs in", name, got)
		}
	}
}

// What a hook in a linked worktree does to a guard: GIT_DIR names the real
// repository, and a fixture's git writes there rather than where it runs.
func TestAFixtureLeavesTheRepositoryAHookRunsInAlone(t *testing.T) {
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	real, fixture := t.TempDir(), t.TempDir()
	git(real, "init", "-q")
	t.Setenv("GIT_DIR", filepath.Join(real, ".git"))
	Isolate()
	git(fixture, "init", "-q")
	git(fixture, "config", "user.name", "a fixture")

	config, err := os.ReadFile(filepath.Join(real, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(config), "a fixture") {
		t.Errorf("a fixture wrote to the repository the hook runs in:\n%s", config)
	}
}

func TestIsCommitAcceptsOnlyAFullLowerCaseHash(t *testing.T) {
	full := "0123456789abcdef0123456789abcdef01234567"
	for s, want := range map[string]bool{
		full:           true,
		full[:39]:      false,
		full + "0":     false,
		"0123456":      false,
		"main":         false,
		"v1.2.3":       false,
		"":             false,
		"G" + full[1:]: false,
		"0123456789ABCDEF0123456789ABCDEF01234567": false,
	} {
		if got := IsCommit(s); got != want {
			t.Errorf("IsCommit(%q) = %v, want %v", s, got, want)
		}
	}
}
