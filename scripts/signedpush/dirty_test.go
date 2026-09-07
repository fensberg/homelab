package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A dirty tree is refused before anything is published, not after.
//
// The check existed, inside syncLocal, which runs after the commits are signed
// and the branch moved. A dirty tree therefore produced the worst outcome
// available: published remotely, not synced locally, permanently diverged - and
// non_fast_forward covers every branch here, so the repair is deleting the
// branch and recreating the pull request.
//
// It also catches a commit that silently failed. A mangled message, a refusing
// hook, a shell chain that swallowed the exit status: each leaves work in the
// tree and a publish about to go out without it.
func TestADirtyTreeIsRefused(t *testing.T) {
	// Stated rather than inherited, so this reports the code and not whatever
	// git configuration the machine carries.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")

	dir := gitRepo(t)
	t.Chdir(dir)

	if err := refuseDirtyTree(); err != nil {
		t.Fatalf("a clean tree was refused: %v", err)
	}

	// Untracked files are not dirty: toolshed/ holds built binaries by design,
	// and treating them as dirty blocked a safe publish once already.
	if err := os.WriteFile(filepath.Join(dir, "untracked.bin"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := refuseDirtyTree(); err != nil {
		t.Errorf("an untracked file was treated as a dirty tree: %v", err)
	}

	// A modified tracked file is.
	tracked := filepath.Join(dir, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "tracked.txt"}, {"commit", "-q", "-m", "add"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if err := refuseDirtyTree(); err != nil {
		t.Fatalf("a clean tree was refused after committing: %v", err)
	}

	if err := os.WriteFile(tracked, []byte("second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := refuseDirtyTree()
	if err == nil {
		t.Fatal("a modified tracked file was not refused, so a publish would go out without it")
	}
	if !strings.Contains(err.Error(), "tracked.txt") {
		t.Errorf("the refusal does not name the file, so nobody can tell what was left behind:\n%v", err)
	}
}
