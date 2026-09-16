package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every program is built into one ignored directory.
//
// The taskfile builds rather than using `go run`, deliberately: `go run`'s
// wrapper swallows SIGINT, which orphaned real infrastructure once. The cost of
// that choice used to be a binary beside each program's source and one ignore
// rule per program - individually forgettable, and two were forgotten. The
// canary never had one, and survey's was added after the binary was already
// tracked, so it never took effect and 3.6MB of compiled Go sat on main until a
// check went looking.
//
// One directory cannot be forgotten for a program nobody thought about. These
// assert the arrangement holds rather than trusting that the next person
// follows it.
func TestNothingIsBuiltOutsideTheToolshed(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "taskfile.yml"))
	if err != nil {
		t.Fatalf("reading taskfile.yml: %v", err)
	}

	found := 0
	for _, m := range goBuildTarget.FindAllStringSubmatch(string(body), -1) {
		target := m[1]
		if target == "/dev/null" {
			continue // a build that only proves it compiles
		}
		found++
		if !strings.Contains(target, "toolshed/") {
			t.Errorf("taskfile.yml builds a binary to %q, outside the toolshed.\n\n"+
				"Then it needs its own ignore rule, which is the arrangement that let a "+
				"binary reach main. Build into toolshed/ instead.", target)
		}
	}
	if found == 0 {
		t.Fatal("no builds found in taskfile.yml, so this guards nothing - and passing " +
			"on an empty set is how a check stops mattering")
	}
}

func TestTheToolshedIsIgnored(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), ".gitignore"))
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}
	if !regexp.MustCompile(`(?m)^toolshed/?$`).MatchString(string(body)) {
		t.Fatal("toolshed/ is not ignored, so every build leaves untracked binaries and " +
			"the next `git add -A` commits them")
	}
}

// A compiled binary that is neither tracked nor ignored is one `git add -A`
// away from being committed.
//
// TestNoCompiledBinaryIsTracked below catches the HARM. This catches the
// PRECONDITION, which is the only moment it is still cheap to fix: once a
// binary is tracked and pushed it cannot be removed, because non_fast_forward
// applies to every branch here, so the remedy is a new branch and a new pull
// request. That has already cost one (#167).
//
// The window is real and narrow. Checking out a branch that predates the
// toolshed move builds in place, and returning to a toolshed branch leaves the
// binary at a path .gitignore no longer covers - it shows as `??` rather than
// ignored. `.gitignore` keeps the three old paths for exactly that reason, with
// a trigger for deleting them; this is the other half, and it covers a path
// nobody thought to keep.
//
// It will occasionally fire on somebody's leftovers, which is arguably the
// point: an untracked 9 MB executable in the working tree is worth being told
// about whether or not it was about to be committed.
func TestNoCompiledBinaryIsUntrackedAndUnignored(t *testing.T) {
	root := repoRoot(t)

	out, err := exec.Command("git", "-C", root, "ls-files",
		"--others", "--exclude-standard", "-z").Output()
	if err != nil {
		// NOT a skip, except in the one place where it is provably right.
		//
		// This skipped on any git error at all, which is fail-open: a guard
		// that cannot ask its question reports the same green as one that
		// asked and found nothing. "I could not look" and "there is nothing
		// there" are different facts and only one is reassuring.
		//
		// The mutation ledger copies tracked files into a scratch directory
		// that is deliberately not a git repository, and it announces that by
		// setting repoRootEnv. That case is known, named, and the only one.
		// Anywhere else, a checkout git will not answer about is a broken
		// environment and saying so is the point.
		if os.Getenv(repoRootEnv) != "" {
			t.Skip("inner run: the ledger's scratch tree is not a git repository, " +
				"so there is no working tree to ask about")
		}
		t.Fatalf(`git could not list untracked files in %s: %v

This guard cannot answer its question here, and a guard that cannot look must
say so rather than pass. If this is a checkout, something is wrong with it.`, root, err)
	}

	for _, path := range strings.Split(string(out), "\x00") {
		if path == "" || strings.Contains(path, ".") {
			continue
		}
		info, statErr := os.Stat(filepath.Join(root, path))
		if statErr != nil || info.IsDir() || info.Size() < 1<<20 {
			continue // a megabyte of extensionless file is the signature
		}
		if info.Mode()&0o111 == 0 {
			continue
		}
		t.Errorf(`%s is an executable of %d bytes that git neither tracks nor ignores.

The next `+"`git add -A`"+` takes it. A tracked binary cannot be removed from a
pushed commit - non_fast_forward applies to every branch here - so the remedy
is a new branch and a new pull request, which has already cost one.

Builds belong in toolshed/, which is ignored wholesale. If this is a leftover
from a branch that predates that move, deleting it is enough; if something is
still building to this path, point it at toolshed/ instead.`, path, info.Size())
	}
}

// The ignore rule stops the next one. This catches one that got in before the
// rule existed - which is exactly what happened to survey.
func TestNoCompiledBinaryIsTracked(t *testing.T) {
	root := repoRoot(t)
	out, err := exec.Command("git", "-C", root, "ls-files").Output()
	if err != nil {
		t.Fatalf("listing tracked files: %v", err)
	}
	for _, path := range strings.Split(string(out), "\n") {
		if path == "" || strings.HasSuffix(path, ".go") || strings.Contains(path, ".") {
			continue
		}
		info, err := os.Stat(filepath.Join(root, path))
		if err != nil || info.IsDir() || info.Size() < 1<<20 {
			continue // a megabyte of extensionless file is the signature
		}
		if info.Mode()&0o111 == 0 {
			continue
		}
		t.Errorf("%s is a tracked executable of %d bytes. A compiled binary changes on "+
			"every build, bloats the history, and cannot be removed from a pushed commit "+
			"because non_fast_forward applies to every branch here.", path, info.Size())
	}
}

// `go build -C <dir> -o <target> .`
var goBuildTarget = regexp.MustCompile(`go build -C \S+ -o (\S+)`)
