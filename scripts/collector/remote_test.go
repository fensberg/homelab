package main

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// fixtureWithRemotes builds a repository whose remote-tracking refs describe
// the branches GitHub was found to hold on 2026-09-11: main, a branch whose pull
// request was closed without merging, and a branch a workflow pushed with no
// pull request at all.
//
// User configuration is neutralised because everything here shells to git, and
// a fixture that inherits commit.gpgsign or a hooks path reports the machine
// rather than the code.
func fixtureWithRemotes(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Chdir(t.TempDir())
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=a@example.com", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "base"},
		{"update-ref", "refs/remotes/origin/main", "HEAD"},
		{"update-ref", "refs/remotes/origin/feat/closed", "HEAD"},
		{"update-ref", "refs/remotes/origin/revert/converge-failed-abcdef12", "HEAD"},
		{"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestRemoteBranchesAreReadFromTheRemoteTrackingRefs(t *testing.T) {
	fixtureWithRemotes(t)
	names, err := remoteBranches()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(names, " ")
	want := "feat/closed main revert/converge-failed-abcdef12"
	if got != want {
		t.Errorf("read %q, want %q - origin/HEAD is a pointer, not a branch, and must not be listed", got, want)
	}
}

// A dry run names what would go and what stays, and why, and changes nothing.
// It is the form a person reads before adding -apply, so it has to be exact:
// the closed pull request's branch would go, the unexplained one is kept by
// name, and main is not mentioned as collectable at all.
func TestADryRunOverRemoteBranchesChangesNothingAndSaysWhy(t *testing.T) {
	fixtureWithRemotes(t)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	collectRemote(false, map[string]bool{}, map[string]bool{"feat/closed": true}, map[string]bool{})
	os.Stdout = stdout
	w.Close()
	outBytes, _ := io.ReadAll(r)
	out := string(outBytes)

	for _, want := range []string{"would collect  feat/closed", "pull request closed", "kept           revert/converge-failed-abcdef12", "no pull request"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run did not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "collect  main") || strings.Contains(out, "collected") {
		t.Errorf("a dry run collected something, or offered main:\n%s", out)
	}
	if names, _ := remoteBranches(); len(names) != 3 {
		t.Errorf("the dry run changed the remote-tracking refs: %v", names)
	}
}
