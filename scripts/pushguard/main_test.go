package main

import (
	"os"
	"os/exec"
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

// git runs a command in dir and returns its trimmed output, failing the test
// on error. Enough git for a fixture repository and no more.
func git(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// fixtureRepo builds a repository with one unsigned commit and one carrying a
// signature header, and returns their hashes.
//
// The signed commit is written directly with `git hash-object` rather than by
// signing, so the test needs no key, no ssh-keygen and no crypto - it is
// checking that a signature header is *noticed*, which is exactly what the
// guard checks. That also keeps this hermetic: git and nothing else.
func fixtureRepo(t *testing.T) (base, unsigned, signed string) {
	t.Helper()
	t.Chdir(t.TempDir())

	git(t, "init", "-q", "-b", "main", ".")
	git(t, "config", "user.email", "fixture@example.invalid")
	git(t, "config", "user.name", "Fixture")
	// The unsigned commit below has to actually be unsigned.
	//
	// A machine with commit.gpgsign set globally - which anyone following this
	// repository's own setup will have - signs every commit including this
	// fixture's, so the test finds nothing unsigned and fails while the code is
	// perfectly correct. It passed only on a machine with signing switched off,
	// which is the one configuration this repository tells people not to have.
	git(t, "config", "commit.gpgsign", "false")
	git(t, "commit", "-q", "--allow-empty", "-m", "base")
	base = git(t, "rev-parse", "HEAD")
	git(t, "commit", "-q", "--allow-empty", "-m", "unsigned")
	unsigned = git(t, "rev-parse", "HEAD")

	tree := git(t, "rev-parse", "HEAD^{tree}")
	raw := "tree " + tree + "\n" +
		"parent " + unsigned + "\n" +
		"author Fixture <fixture@example.invalid> 0 +0000\n" +
		"committer Fixture <fixture@example.invalid> 0 +0000\n" +
		"gpgsig placeholder-header-value\n" +
		"\nsigned\n"

	cmd := exec.Command("git", "hash-object", "-t", "commit", "-w", "--stdin")
	cmd.Stdin = strings.NewReader(raw)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("writing the signed fixture commit: %v\n%s", err, out)
	}
	signed = strings.TrimSpace(string(out))
	git(t, "update-ref", "refs/heads/main", signed)
	return base, unsigned, signed
}

// The behaviour the guard exists for, against real commit objects rather than
// against an unreadable range: an unsigned commit in the push is refused, and
// a signed one is not.
//
// This was verified by hand when the guard was rewritten and not committed,
// which left the one behaviour that actually changed with no regression cover
// at all. The ref-prefix tests above would all still pass if the signature
// check were deleted outright.
func TestAnUnsignedCommitIsRefusedAndASignedOneIsNot(t *testing.T) {
	base, unsigned, signed := fixtureRepo(t)

	// A push that adds the unsigned commit.
	got, err := unsignedCommits(base, unsigned)
	if err != nil {
		t.Fatalf("reading a range containing an unsigned commit failed: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %v, want exactly the one unsigned commit in the push", got)
	}

	// The signed commit alone must come back clean.
	clean, err := unsignedCommits(unsigned, signed)
	if err != nil {
		t.Fatalf("reading a range of one signed commit failed: %v", err)
	}
	if len(clean) != 0 {
		t.Errorf("a commit carrying a signature header was reported unsigned: %v\n"+
			"That refuses exactly the commits somebody went to the trouble of signing.", clean)
	}
}

// The trap this guard fell into once and must not fall into again.
//
// `git log --format=%G?` reports N for a signed commit whenever it cannot
// verify the signature - which for SSH signing is any repository without
// gpg.ssh.allowedSignersFile. Presence and trust are different questions, and
// only presence is this hook's business.
func TestSignatureDetectionDoesNotDependOnVerifiability(t *testing.T) {
	_, _, signed := fixtureRepo(t)

	// Nothing here can verify that header. git may report N, or refuse to
	// parse it at all - both are "cannot verify", and neither is "unsigned".
	// Run tolerantly, because the failure is the demonstration.
	out, err := exec.Command("git", "log", "-1", "--format=%G?", signed).CombinedOutput()
	t.Logf("git's own verdict: %q (err: %v) - not usable as a signedness test",
		strings.TrimSpace(string(out)), err)

	has, err := hasSignature(signed)
	if err != nil {
		t.Fatalf("reading the commit object failed: %v", err)
	}
	if !has {
		t.Error("a commit with a signature header was read as unsigned.\n" +
			"Verification is GitHub's question and it holds the keys; presence is " +
			"this hook's question and the commit object answers it unconditionally.")
	}
}
