package repo

import (
	"strings"
	"testing"
)

// Every program is built into toolshed/, which is ignored wholesale; that the
// taskfile builds there and .gitignore ignores it is a configuration rule
// (policy/conftest/toolshed.rego). These are the half only git can answer.

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
	// The scratch tree a meta test judges is a git repository too, so this
	// asks git everywhere, and a git that cannot answer fails the test.
	tree := repository(t)
	for _, path := range tree.Untracked(t) {
		if strings.Contains(path, ".") {
			continue
		}
		info, statErr := tree.Stat(path)
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
	tree := repository(t)
	files := tree.Files(t)
	const atLeastAHundredTracked = 100
	if len(files) < atLeastAHundredTracked {
		t.Fatalf("only %d tracked files were listed, so this looked at almost nothing", len(files))
	}
	for _, path := range files {
		if strings.HasSuffix(path, ".go") || strings.Contains(path, ".") {
			continue
		}
		info, err := tree.Stat(path)
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
