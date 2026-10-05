package phases

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homelab/contractor/config"
	"homelab/contractor/internal/run"
)

// Functions a test reached and did not run.
//
// Each of these had a statement or two executed by some other test - a guard
// at the top, an early return - and so counted as tested while the work it
// exists to do had never run. The coverage ledger counted any statement at
// all until it was held to more than half (tests/go/repo). These are the
// tests that were owed when it was.

// programOnPath puts a shell script on PATH under a program's name, ahead of
// whatever is there already, and returns the directory it keeps its files in.
func programOnPath(t *testing.T, name, script string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// printed is what a function writes to standard output.
func printed(t *testing.T, do func()) string {
	t.Helper()
	was := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = was }()
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	do()
	w.Close()
	return <-done
}

// --- a converge that is no longer the tip of main -----------------------------

// gitSaying is a git whose HEAD and whose origin's main are what the files in
// its directory say.
func gitSaying(t *testing.T, head, remote string) string {
	t.Helper()
	dir := programOnPath(t, "git", `d=$(dirname "$0")
case "$1" in
  rev-parse) cat "$d/head" ;;
  ls-remote) [ -f "$d/origin_unreachable" ] && exit 128; cat "$d/remote" ;;
esac
`)
	mustWriteFile(t, filepath.Join(dir, "head"), head)
	mustWriteFile(t, filepath.Join(dir, "remote"), remote)
	return dir
}

func TestACheckoutAtTheTipOfMainIsCurrent(t *testing.T) {
	gitSaying(t, "aaaa111122223333\n", "aaaa111122223333\trefs/heads/main\n")
	if err := checkoutIsCurrent(); err != nil {
		t.Fatalf("the tip of main was refused: %v", err)
	}
}

// Main moved on while this converge waited for a runner. It is not a failure
// and must not be reported as one: nothing is applied, and the newer commit
// converges on its own.
func TestACheckoutMainHasMovedPastIsSuperseded(t *testing.T) {
	gitSaying(t, "aaaa111122223333\n", "bbbb444455556666\trefs/heads/main\n")
	var superseded *SupersededError
	if err := checkoutIsCurrent(); !errors.As(err, &superseded) {
		t.Fatalf("got %v, want a converge that was superseded", err)
	}
	if !strings.HasPrefix("aaaa111122223333", superseded.Head) || !strings.HasPrefix("bbbb444455556666", superseded.Tip) {
		t.Errorf("it names %q and %q, which are not the two commits", superseded.Head, superseded.Tip)
	}
}

// Not being able to tell is not the same as being current.
func TestACheckoutThatCannotAskOriginIsRefusedAndNotSuperseded(t *testing.T) {
	dir := gitSaying(t, "aaaa111122223333\n", "")
	mustWriteFile(t, filepath.Join(dir, "origin_unreachable"), "")
	err := checkoutIsCurrent()
	var superseded *SupersededError
	if err == nil || errors.As(err, &superseded) || !strings.Contains(err.Error(), "could not read the published tip") {
		t.Fatalf("got %v, want a refusal that says it could not tell", err)
	}
}

func TestAnOriginWithNoMainIsRefused(t *testing.T) {
	gitSaying(t, "aaaa111122223333\n", "")
	if err := checkoutIsCurrent(); err == nil || !strings.Contains(err.Error(), "no main branch") {
		t.Fatalf("got %v", err)
	}
}

// --- which commit a plan says it describes ------------------------------------

// gitOfACheckout is a git in a checkout whose HEAD is a merge commit when
// it has a second parent, as a pull request's is.
func gitOfACheckout(t *testing.T, secondParent string) {
	t.Helper()
	programOnPath(t, "git", `case "$*" in
  "rev-parse --short HEAD^2") [ -n "`+secondParent+`" ] || exit 128; echo "`+secondParent+`" ;;
  "rev-parse --short HEAD") echo "head000" ;;
esac
`)
	t.Setenv("GITHUB_EVENT_PATH", "")
}

// On a pull request HEAD is the merge commit, which nobody would recognise.
// The commit a reader knows is its second parent.
func TestAPlanOfAPullRequestNamesTheCommitThatWasPushed(t *testing.T) {
	gitOfACheckout(t, "pushed1")
	t.Setenv("GITHUB_EVENT_NAME", "pull_request")
	if got := plannedCommit(); got != "pushed1" {
		t.Fatalf("the plan says it describes %q", got)
	}
}

// And where that cannot be read, a pull request's plan names no commit rather
// than the merge commit: a wrong one is worse than none.
func TestAPlanOfAPullRequestNamesNothingRatherThanTheMergeCommit(t *testing.T) {
	gitOfACheckout(t, "")
	t.Setenv("GITHUB_EVENT_NAME", "pull_request")
	if got := plannedCommit(); got != "" {
		t.Fatalf("the plan says it describes %q, which is the merge commit", got)
	}
}

func TestAPlanOutsideAPullRequestNamesHEAD(t *testing.T) {
	gitOfACheckout(t, "")
	t.Setenv("GITHUB_EVENT_NAME", "push")
	if got := plannedCommit(); got != "head000" {
		t.Fatalf("the plan says it describes %q", got)
	}
}

// --- what a restore shows of the bucket ---------------------------------------

func TestTheBackupsInTheBucketAreListed(t *testing.T) {
	f := newVerbFixture(t)
	programOnPath(t, "rclone", `case "$3" in lsl) printf '   1024 2026-10-01 12:00:00 latest.tfstate.age\n\n   1024 2026-10-01 12:00:00 20261001-120000.tfstate.age\n' ;; esac
`)
	out := printed(t, func() { listBackups(f.ctx, nil, "R2:bucket/management-cluster") })
	for _, want := range []string{"latest.tfstate.age", "20261001-120000.tfstate.age"} {
		if !strings.Contains(out, want) {
			t.Errorf("%s is in the bucket and was not listed:\n%s", want, out)
		}
	}
}

// A bucket that cannot be listed says nothing: the restore that follows is
// what reports it, with the reason.
func TestABucketThatCannotBeListedPrintsNothing(t *testing.T) {
	f := newVerbFixture(t)
	programOnPath(t, "rclone", "exit 1\n")
	if out := printed(t, func() { listBackups(f.ctx, nil, "R2:bucket/management-cluster") }); strings.Contains(out, "backups in the bucket") {
		t.Errorf("a listing that failed was shown as a list:\n%s", out)
	}
}

// --- a standalone backup attaches first ---------------------------------------

// A workspace nothing has initialised is attached to the state before a
// backup reads it. The initialised case has its own tests; this is the one
// where something has to happen.
func TestABackupOfADetachedWorkspaceAttachesFirst(t *testing.T) {
	f := newVerbFixture(t)
	if err := os.RemoveAll(filepath.Join(f.ctx.Dir, ".terraform", "providers")); err != nil {
		t.Fatal(err)
	}
	if err := attachIfDetached(f.ctx); err != nil {
		t.Fatal(err)
	}
	tofu, _ := f.calls(t)
	if first(tofu, func(c call) bool {
		return strings.HasPrefix(c.args, "init") && strings.Contains(c.args, "-reconfigure")
	}) < 0 {
		t.Errorf("the workspace was not attached to the state it is about to read: %+v", tofu)
	}
}

// --- an image a failed run left behind ----------------------------------------

func orphanFixture(t *testing.T, stored string, removeErr error) (f *verbFixture, cfg *config.Config, net *config.SiteNetwork, removed *[]string) {
	t.Helper()
	f = newVerbFixture(t)
	cfg, err := config.LoadRendered(f.ctx.ConfigRendered)
	if err != nil {
		t.Fatal(err)
	}
	net, err = config.ResolveSiteNetwork(cfg, f.ctx.Site)
	if err != nil {
		t.Fatal(err)
	}
	wasStored, wasRemove := storedImage, removeImage
	t.Cleanup(func() { storedImage, removeImage = wasStored, wasRemove })
	storedImage = func(config.Hypervisor, config.Node, string) (string, error) { return stored, nil }
	removed = &[]string{}
	removeImage = func(_ config.Hypervisor, _ config.Node, volID string) error {
		*removed = append(*removed, volID)
		return removeErr
	}
	return f, cfg, net, removed
}

// The state does not track it and the datastore holds it: a run that died
// left it, and it is deleted so this one can download a copy it can trust.
func TestAnImageAFailedRunLeftBehindIsRemoved(t *testing.T) {
	f, cfg, net, removed := orphanFixture(t, "local-iso:iso/talos-nocloud.img", nil)
	if err := reclaimOrphanedDiskImage(f.ctx, cfg, net); err != nil {
		t.Fatal(err)
	}
	if strings.Join(*removed, ",") != "local-iso:iso/talos-nocloud.img" {
		t.Fatalf("removed %v", *removed)
	}
}

func TestADatastoreWithNoImageHasNothingRemoved(t *testing.T) {
	f, cfg, net, removed := orphanFixture(t, "", nil)
	if err := reclaimOrphanedDiskImage(f.ctx, cfg, net); err != nil {
		t.Fatal(err)
	}
	if len(*removed) != 0 {
		t.Fatalf("removed %v from a datastore holding nothing of this site's", *removed)
	}
}

// What cannot be removed is named, with the command that removes it by hand.
func TestAnImageThatCannotBeRemovedSaysHowToRemoveIt(t *testing.T) {
	f, cfg, net, _ := orphanFixture(t, "local-iso:iso/talos-nocloud.img", errors.New("the datastore is locked"))
	err := reclaimOrphanedDiskImage(f.ctx, cfg, net)
	if err == nil {
		t.Fatal("an image that could not be removed was reported as removed")
	}
	for _, want := range []string{"the datastore is locked", "pvesm free local-iso:iso/talos-nocloud.img"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%v", want, err)
		}
	}
}

// --- a credential that lives as long as one command ---------------------------

// The command sees the credential, at a path only its owner can read; its
// exit code comes back; and the file is gone when it has.
func TestACredentialLivesExactlyAsLongAsTheCommandGivenIt(t *testing.T) {
	seen := filepath.Join(t.TempDir(), "seen")
	write := func(_ *run.Context, path string) error { return os.WriteFile(path, []byte("the credential"), 0o600) }

	code, err := withRenderedCredential(&run.Context{}, []string{"sh", "-c",
		`cat "$A_CREDENTIAL" > "` + seen + `.body"; stat -c %a "$A_CREDENTIAL" > "` + seen + `.mode"; echo "$A_CREDENTIAL" > "` + seen + `.path"; exit 7`},
		"A_CREDENTIAL", "credential", write)
	if err != nil {
		t.Fatal(err)
	}
	if code != 7 {
		t.Errorf("the command exited 7 and %d was passed on: a wrapper that swallows an exit code hides failures", code)
	}
	read := func(suffix string) string {
		b, err := os.ReadFile(seen + suffix)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	if read(".body") != "the credential" {
		t.Errorf("the command read %q", read(".body"))
	}
	if read(".mode") != "600" {
		t.Errorf("the credential was mode %s while the command ran", read(".mode"))
	}
	if _, err := os.Stat(read(".path")); err == nil {
		t.Error("the credential outlived the command it was rendered for")
	}
}

func TestACredentialIsNotRenderedForNoCommand(t *testing.T) {
	rendered := false
	_, err := withRenderedCredential(&run.Context{}, nil, "A_CREDENTIAL", "credential",
		func(*run.Context, string) error { rendered = true; return nil })
	if err == nil || rendered {
		t.Fatalf("got %v, rendered %v", err, rendered)
	}
}

// A credential that could not be rendered runs nothing, and is not left as an
// empty file for the next command to trip over.
func TestACommandIsNotRunWithACredentialThatCouldNotBeRendered(t *testing.T) {
	ran := filepath.Join(t.TempDir(), "ran")
	var path string
	_, err := withRenderedCredential(&run.Context{}, []string{"sh", "-c", ": > " + ran}, "A_CREDENTIAL", "credential",
		func(_ *run.Context, p string) error { path = p; return errors.New("the state could not be reached") })
	if err == nil {
		t.Fatal("a failed render was reported as a command that ran")
	}
	if _, statErr := os.Stat(ran); statErr == nil {
		t.Error("the command ran with no credential")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("the file a failed render was meant for was left behind")
	}
}

// --- a teardown that does not go to plan --------------------------------------

// askedToDestroy reads the fixture's record of what was run. No record at
// all is no program run, which here is the answer wanted.
func askedToDestroy(f *verbFixture) bool {
	ran, err := os.ReadFile(filepath.Join(f.dir, "calls"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(ran), "\n") {
		if parts := strings.Split(line, "|"); len(parts) == 4 && strings.HasPrefix(parts[3], "destroy") {
			return true
		}
	}
	return false
}

// Nothing to destroy is safe to clean up after: there is no state a retry
// would need.
func TestATeardownWithNoStateDestroysNothingAndMaySterilize(t *testing.T) {
	f := newVerbFixture(t)
	res := tearDown(f.ctx)
	if res.Destroyed || !res.SafeToSterilize {
		t.Fatalf("the teardown reported %+v", res)
	}
	if askedToDestroy(f) {
		t.Error("tofu was asked to destroy with no state to destroy from")
	}
}

// The lesson this function was written around: after a destroy that failed,
// the state is the only way to retry it, and sterilizing would delete it.
func TestATeardownWhoseDestroyFailsMustNotBeSterilized(t *testing.T) {
	f := newVerbFixture(t)
	mustWriteFile(t, f.ctx.LocalState, realisticState)
	programOnPath(t, "tofu", `case "$1" in destroy) echo '{"@level":"error","@message":"Error: the hypervisor refused"}'; exit 1 ;; esac
exec `+filepath.Join(f.dir, "tofu")+` "$@"
`)
	res := tearDown(f.ctx)
	if res.Destroyed || res.SafeToSterilize {
		t.Fatalf("a destroy that failed was reported as %+v: sterilizing now deletes the only way to retry it", res)
	}
	if _, err := os.Stat(f.ctx.LocalState); err != nil {
		t.Error("the state did not survive a destroy that failed")
	}
}

// State that cannot be brought out of a database that is already gone stops
// the teardown before it destroys anything it could not then record.
func TestATeardownThatCannotReachItsStateDestroysNothing(t *testing.T) {
	f := newVerbFixture(t)
	mustWriteFile(t, f.ctx.BackendPgOn, "terraform {}\n")
	was := awaitTCP
	t.Cleanup(func() { awaitTCP = was })
	awaitTCP = func(string, time.Duration, time.Duration) bool { return false }

	res := tearDown(f.ctx)
	if res.Destroyed || res.SafeToSterilize {
		t.Fatalf("the teardown reported %+v", res)
	}
	if askedToDestroy(f) {
		t.Error("tofu was asked to destroy while the state it would record in was unreachable")
	}
}

// --- a plan, from the top -----------------------------------------------------

// The whole phase, against a tofu that plans no change: it says so, and
// writes the comment a pull request shows, with the commit it describes.
func TestAPlanWritesThePullRequestsCommentWithTheCommitItDescribes(t *testing.T) {
	f := newVerbFixture(t)
	programOnPath(t, "tofu", `case "$1" in
  show) echo '{"format_version":"1.2","resource_changes":[]}'; exit 0 ;;
  state) case "$2" in pull) echo '{"version": 4, "serial": 7, "lineage": "a-lineage", "resources": []}'; exit 0 ;; push) cat > /dev/null; exit 0 ;; esac ;;
esac
exec `+filepath.Join(f.dir, "tofu")+` "$@"
`)
	gitOfACheckout(t, "")
	t.Setenv("GITHUB_EVENT_NAME", "push")
	f.ctx.CommentOut = filepath.Join(t.TempDir(), "comment.md")

	var err error
	out := printed(t, func() { err = Plan(f.ctx) })
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	comment, readErr := os.ReadFile(f.ctx.CommentOut)
	if readErr != nil {
		t.Fatalf("no comment was written: %v", readErr)
	}
	for _, want := range []string{commentMarker(f.ctx.Site), "head000"} {
		if !strings.Contains(string(comment), want) {
			t.Errorf("the comment does not carry %q:\n%s", want, comment)
		}
	}
}
