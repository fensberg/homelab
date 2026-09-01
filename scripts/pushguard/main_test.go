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
	// An unsigned commit on a branch is the thing being refused. The range is
	// unreadable in this bare test environment, which fails closed - and
	// failing closed is itself the property worth asserting.
	for _, ref := range []string{
		"refs/heads/main",
		"refs/heads/docs/some-branch",
	} {
		if err := run(ref); err == nil {
			t.Fatalf("run(%q) allowed a branch update it could not vet", ref)
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

// The refusal has to name both routes. Telling the human to use `task push` is
// wrong - it reads a key inside the agent's home that they cannot open - and
// telling the agent to sign locally is wrong, because it has no user account
// and so no signing key. One message, two remedies.
func TestTheRefusalNamesBothWaysToSign(t *testing.T) {
	msg := refused("some-branch", []string{"abc12345"})

	if !strings.Contains(msg, "commit.gpgsign") {
		t.Error("the refusal does not tell a human how to sign locally, which is " +
			"the route available to anyone with a user account")
	}
	if !strings.Contains(msg, "task push") {
		t.Error("the refusal does not name the agent's route, which is the only " +
			"one available to something with no user account")
	}
	if !strings.Contains(msg, "abc12345") {
		t.Error("the refusal does not name the offending commits, so there is " +
			"nothing to act on")
	}
}

// Being unable to read the commits is not evidence that they are signed.
func TestAnUnreadableRangeFailsClosed(t *testing.T) {
	if _, err := unsignedCommits("not-a-ref", "also-not-a-ref"); err == nil {
		t.Fatal("an unreadable range reported no unsigned commits, which would " +
			"wave a push through because the guard could not look")
	}
}
