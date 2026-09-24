package repopath

import (
	"os"
	"path/filepath"
	"testing"
)

// The root is found at any depth, which is the property the eight copies it
// replaces did not have.
func TestRootIsFoundFromAnyDepth(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, Marker), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, depth := range []string{
		root,
		filepath.Join(root, "a"),
		filepath.Join(root, "a", "b", "c", "d", "e"),
	} {
		if err := os.MkdirAll(depth, 0o700); err != nil {
			t.Fatal(err)
		}
		got, err := rootFrom(depth)
		if err != nil {
			t.Errorf("from %s: %v", depth, err)
			continue
		}
		if got != root {
			t.Errorf("from %s found %s, want %s", depth, got, root)
		}
	}
}

// Outside a checkout it says so, rather than returning some directory that
// happens to be the top of the filesystem.
func TestRootRefusesWhenThereIsNoMarker(t *testing.T) {
	if got, err := rootFrom(t.TempDir()); err == nil {
		t.Errorf("found a root at %s with no %s anywhere above it", got, Marker)
	}
}

// And against the real repository, the answer is the real repository.
func TestRootFindsThisRepository(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "management", "cluster")); err != nil {
		t.Errorf("Root() returned %s, which has no management/cluster: %v", root, err)
	}
}

// Join is Root plus a path, and lands inside the repository.
func TestJoinResolvesInsideTheRepository(t *testing.T) {
	p, err := Join("management", "cluster")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("Join(management, cluster) = %s, which does not exist: %v", p, err)
	}
}
