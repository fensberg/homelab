package main

import (
	"os"
	"strings"
	"testing"
)

// The scratch ref signedpush actually uses must be allowed, or `task push`
// breaks and the guard has made the correct path harder than the wrong one.
func TestScratchRefIsAllowed(t *testing.T) {
	for _, ref := range []string{
		"refs/signing/0a1b2c3d",
		"refs/signing/deadbeef",
	} {
		if err := run(ref); err != nil {
			t.Errorf("run(%q) refused signedpush's own push: %v", ref, err)
		}
	}
}

// The whole point: a direct branch update is what produces an unsigned commit.
func TestBranchUpdateIsRefused(t *testing.T) {
	for _, ref := range []string{
		"refs/heads/main",
		"refs/heads/docs/some-branch",
	} {
		err := run(ref)
		if err == nil {
			t.Fatalf("run(%q) allowed a direct branch update", ref)
		}
		if !strings.Contains(err.Error(), "task push") {
			t.Errorf("run(%q) refused without naming the remedy: %v", ref, err)
		}
	}
}

// A guard that fails open is not a guard. If the hook cannot tell what is
// being pushed, it must refuse rather than assume the safe case.
func TestUnknownRefIsRefused(t *testing.T) {
	err := run("")
	if err == nil {
		t.Fatal("an undetermined ref was allowed; the guard fails open")
	}
	if !strings.Contains(err.Error(), remoteRefEnv) {
		t.Errorf("refusal does not name the missing variable: %v", err)
	}
}

// Tags are not a route to an unsigned branch commit, and refusing them would
// be friction with nothing behind it.
func TestNonBranchRefsAreNotThisHooksBusiness(t *testing.T) {
	if err := run("refs/tags/v1.0.0"); err != nil {
		t.Errorf("a tag push was refused: %v", err)
	}
}

// The prefix this guard allows must be the prefix signedpush actually pushes
// to. If signedpush changes its scratch namespace, this is the reminder.
func TestScratchPrefixMatchesSignedpush(t *testing.T) {
	const wantInSignedpush = `"refs/signing/"`
	src := readSignedpushSource(t)
	if !strings.Contains(src, wantInSignedpush) {
		t.Fatalf("scripts/signedpush no longer builds its scratch ref from %s;"+
			" pushguard's scratchPrefix (%q) is now guessing", wantInSignedpush, scratchPrefix)
	}
}

// readSignedpushSource reads the sibling command's source. The two are
// separate modules that must agree on one string, and nothing else links
// them, so the coupling is checked here rather than left to be discovered
// the next time a push is silently unsigned.
func readSignedpushSource(t *testing.T) string {
	t.Helper()
	const path = "../signedpush/main.go"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s to check the scratch ref agrees: %v", path, err)
	}
	return string(b)
}
