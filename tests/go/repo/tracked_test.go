package repo

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The one enumeration every guard in this package walks.
//
// WHY THIS EXISTS. Guards here discovered their subjects two different ways and
// the two disagreed. Some asked `git ls-files`; others walked from the root with
// a hand-written `skipDirs` map. The fork was not a style difference - the walk
// existed because the mutation ledger runs these guards against a COPY of the
// tracked files, and that copy was not a git repository, so `git ls-files` there
// exited 128 and the guard reported the harness instead of the code.
//
// The ledger now runs `git init` in that copy (see scratchRepo), which costs a
// few milliseconds and removes the only reason the second idiom existed.
//
// WHY TRACKED FILES ARE THE HONEST UNIVERSE. A walk needs a list of directories
// to skip, and that list is the same decaying artifact this repository refuses
// everywhere else: it names the generated directories that existed when it was
// written, so the next toolchain's output - target/, .venv/, dist/ - is walked
// as though somebody here had authored it. `git ls-files` needs no such list,
// because .gitignore already answers the question and is maintained for its own
// reasons. It is also exactly the set a reviewer sees in a diff.
//
// WHAT THIS DELIBERATELY DOES NOT DO. It does not take a directory to look in.
// A guard that enumerates from a named subtree is green on the day it is written
// and covers less every time something moves, with nothing to say so - which is
// the failure it was built to prevent, wearing the walk's clothes. Filter the
// result by what you are asserting about; never narrow where you looked.

// A repository this size cannot plausibly have fewer tracked files, so a count
// below it means the enumeration broke rather than that the tree shrank. Without
// this, a failed listing is an empty list, and every guard downstream passes by
// examining nothing.
const fewestPlausibleTrackedFiles = 100

// trackedFiles lists every file git tracks, relative to the repository root.
func trackedFiles(t *testing.T) []string {
	t.Helper()
	return trackedFilesIn(t, repoRoot(t))
}

// trackedFilesIn lists the tracked files of a tree other than the repository
// itself - the scratch tree a guard judges when patches are outstanding.
func trackedFilesIn(t *testing.T, root string) []string {
	t.Helper()

	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Fatalf(`listing tracked files in %s: %v

Every guard in this package enumerates from here, so this is not a failure of
one check - nothing downstream can see its subjects. If this is the mutation
ledger's scratch tree, scratchRepo is meant to have run `+"`git init`"+` in it.`, root, err)
	}

	var files []string
	for _, rel := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if rel != "" {
			files = append(files, rel)
		}
	}
	if len(files) < fewestPlausibleTrackedFiles {
		t.Fatalf(`git ls-files returned %d file(s) in %s, and this repository has
many more than that.

The enumeration has broken. Left alone it would report an empty universe, and
every guard reading it would pass by examining nothing - which looks exactly
like everything being correct.`, len(files), root)
	}
	return files
}

// trackedMatching is trackedFiles filtered by what the caller asserts about.
func trackedMatching(t *testing.T, keep func(rel string) bool) []string {
	t.Helper()
	var matched []string
	for _, rel := range trackedFiles(t) {
		if keep(rel) {
			matched = append(matched, rel)
		}
	}
	return matched
}

// Tracked files this repository did not write and does not edit.
//
// This is the half `git ls-files` cannot answer. The generated directories that
// used to be in skipDirs - node_modules, coverage, .terraform - are untracked,
// so they are gone from the enumeration for free. These are tracked: committed
// deliberately, and still somebody else's output. A finding inside one is a
// finding nobody here can act on.
//
// Unlike a walk root, this list is a claim about specific subjects rather than
// about where to look, so naming paths in it is correct. A new entry is a
// decision somebody makes once; a missing entry produces noise, not silence,
// which is the direction this should fail in.
var notAuthoredHere = []string{
	// Flux's own install manifest, committed verbatim - the same exclusion
	// .checkov.yaml already makes.
	"clusters/management/flux-system/",
	// Generated integrity databases and machine-written state.
	"pnpm-lock.yaml",
	"go.sum",
}

// authoredHere reports whether a tracked path is this repository's own work.
func authoredHere(rel string) bool {
	for _, skip := range notAuthoredHere {
		if strings.HasSuffix(skip, "/") {
			if strings.HasPrefix(rel, skip) {
				return false
			}
			continue
		}
		if rel == skip || filepath.Base(rel) == skip {
			return false
		}
	}
	return true
}

// The primitive is itself a guard's subject, because everything else trusts it.
//
// A vacuity floor that never fires proves nothing, and a floor set above the
// real count would fail every run. This pins both ends: the enumeration returns
// a plausible number, and it returns paths relative to the root rather than
// absolute ones, which is what every caller assumes when it matches on prefixes.
func TestTheTrackedEnumerationSeesTheWholeRepository(t *testing.T) {
	files := trackedFiles(t)

	for _, rel := range files {
		if filepath.IsAbs(rel) {
			t.Fatalf("git ls-files returned the absolute path %q; callers match on "+
				"repository-relative prefixes and would silently match nothing", rel)
		}
	}

	// Directories that exist for different reasons, so a walk that had stopped
	// at one subtree could not satisfy all of them at once.
	for _, dir := range []string{
		"tests/go/repo/",
		"scripts/",
		".github/workflows/",
		"management/cluster/",
		"clusters/",
	} {
		found := false
		for _, rel := range files {
			if strings.HasPrefix(rel, dir) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf(`the tracked enumeration found nothing under %s.

Either that subtree has moved, in which case every guard filtering on this
prefix is now asserting about nothing, or the enumeration is not seeing the
whole repository.`, dir)
		}
	}
}

// The exclusion list describes things that are actually here.
//
// An entry for something that no longer exists makes the excluded set look
// larger than it is, and hides that a guard stopped covering something real.
func TestEveryUnauthoredPathStillExists(t *testing.T) {
	files := trackedFiles(t)

	for _, skip := range notAuthoredHere {
		found := false
		for _, rel := range files {
			if strings.HasSuffix(skip, "/") {
				if strings.HasPrefix(rel, skip) {
					found = true
				}
			} else if rel == skip || filepath.Base(rel) == skip {
				found = true
			}
			if found {
				break
			}
		}
		if !found {
			t.Errorf(`notAuthoredHere excludes %q, which this repository no longer
tracks.

A stale exclusion is a hole nobody can see: it reads as a considered decision
while covering nothing, and the next thing to land at that path inherits the
exemption without anybody choosing it.`, skip)
		}
	}
}

// openTofuSources is every OpenTofu file this repository authored.
//
// Two guards assert about the same set - no break-glass identity, no Flux git
// credential - and both are making the same claim: a value OpenTofu reads is a
// value OpenTofu writes to state. Enumerating it twice is two places for the
// set to drift, and the one that drifts is the one nobody is looking at.
func openTofuSources(t *testing.T) []string {
	t.Helper()
	sources := trackedMatching(t, func(rel string) bool {
		return authoredHere(rel) &&
			(strings.HasSuffix(rel, ".tf") || strings.HasSuffix(rel, ".tftest.hcl"))
	})

	const fewestOpenTofuSources = 5
	if len(sources) < fewestOpenTofuSources {
		t.Fatalf(`only %d OpenTofu source(s) were found, and this estate has more.

The enumeration has stopped matching, and every file it no longer sees is one
the guards reading this silently stopped asking about.`, len(sources))
	}
	return sources
}
