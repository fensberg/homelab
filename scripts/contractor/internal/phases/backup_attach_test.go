package phases

import (
	"os"
	"path/filepath"
	"testing"
)

// A standalone backup has to initialise the workspace it is about to read.
//
// `task backup-state` and the nightly tier both run this phase alone, on a
// workspace that has never been initialised. `tofu state pull` then failed with
// "Required plugins are not installed", naming all five providers - the first
// time the integration tier ever got far enough to try.
func TestADetachedWorkspaceNeedsAttaching(t *testing.T) {
	if !needsAttach(t.TempDir()) {
		t.Error("an empty workspace was treated as ready to pull state")
	}
}

// A full ignition reaches this phase through Migrate, which has already
// initialised the workspace - and it reaches it BEFORE Sterilize, so the local
// state file is still there. Attach refuses to run against local state, on
// purpose, so attaching here unconditionally would break the ignition path in
// order to fix the standalone one.
func TestAnInitialisedWorkspaceIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	providers := filepath.Join(dir, ".terraform", "providers", "registry.opentofu.org")
	if err := os.MkdirAll(providers, 0o755); err != nil {
		t.Fatal(err)
	}

	if needsAttach(dir) {
		t.Error("an initialised workspace would be re-attached, which refuses when local state exists")
	}
}

// A .terraform holding only a backend record is not an initialised workspace.
//
// The directory alone was the first version of this check, and it would have
// passed on exactly the workspace that failed: tofu had written .terraform
// while reading the lock file, and no provider had been downloaded.
func TestADirectoryWithoutProvidersStillNeedsAttaching(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".terraform"), 0o755); err != nil {
		t.Fatal(err)
	}

	if !needsAttach(dir) {
		t.Error("a .terraform with no providers in it was treated as initialised")
	}

	// And an empty providers directory is the same case.
	if err := os.MkdirAll(filepath.Join(dir, ".terraform", "providers"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !needsAttach(dir) {
		t.Error("an empty providers directory was treated as initialised")
	}
}
