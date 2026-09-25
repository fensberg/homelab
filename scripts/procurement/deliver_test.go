package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/details/repopath"
)

const (
	nextDigest  = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	otherDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

// The real releases file, comments and all, so the edit is proved against
// the file it will actually be run on.
func realReleases(t *testing.T) string {
	t.Helper()
	root, err := repopath.Root()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "clusters", "management", "releases.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Exactly two lines change - the named source's tag and digest - so the pull
// request a person reviews is the release and nothing else.
func TestDeliverMovesOnlyThePinsTwoLines(t *testing.T) {
	before := realReleases(t)
	after, changed, err := movePin(before, "valheim", "9.9.9-1", nextDigest)
	if err != nil || !changed {
		t.Fatalf("movePin = changed %v, err %v", changed, err)
	}
	b, a := strings.Split(before, "\n"), strings.Split(after, "\n")
	if len(b) != len(a) {
		t.Fatalf("the file went from %d lines to %d; only two lines may change", len(b), len(a))
	}
	var diff []string
	for i := range b {
		if b[i] != a[i] {
			diff = append(diff, a[i])
		}
	}
	if len(diff) != 2 || !strings.Contains(diff[0]+diff[1], `"9.9.9-1"`) || !strings.Contains(diff[0]+diff[1], nextDigest) {
		t.Errorf("want exactly the tag and digest lines changed, got %q", diff)
	}
}

func TestDeliverMovesOnlyTheNamedWorkload(t *testing.T) {
	two := `---
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: other
  namespace: flux-system
spec:
  ref:
    tag: "1.0.0-1"
    digest: "` + otherDigest + `"
---
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: valheim
  namespace: flux-system
spec:
  ref:
    tag: "1.0.15-5"
    digest: "` + otherDigest + `"
`
	after, _, err := movePin(two, "valheim", "1.0.15-6", nextDigest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(after, `tag: "1.0.0-1"`) || strings.Count(after, otherDigest) != 1 {
		t.Errorf("moving valheim also moved another workload's pin:\n%s", after)
	}
	if !strings.Contains(after, `tag: "1.0.15-6"`) {
		t.Errorf("valheim's pin did not move:\n%s", after)
	}
}

func TestDeliverOfTheReleaseAlreadyPinnedChangesNothing(t *testing.T) {
	before := realReleases(t)
	moved, _, err := movePin(before, "valheim", "9.9.9-1", nextDigest)
	if err != nil {
		t.Fatal(err)
	}
	again, changed, err := movePin(moved, "valheim", "9.9.9-1", nextDigest)
	if err != nil || changed || again != moved {
		t.Errorf("delivering the pinned release again reported a change (%v, %v)", changed, err)
	}
}

// Nothing that is not a release reaches the file: a pin that is not a digest
// is a pin a registry push can move.
func TestDeliverRefusesWhatIsNotARelease(t *testing.T) {
	before := realReleases(t)
	for name, tc := range map[string]struct{ name, tag, digest string }{
		"a tag with no counter":  {"valheim", "1.0.15", nextDigest},
		"a digest that is short": {"valheim", "1.0.15-6", "sha256:abc"},
		"a workload not there":   {"minecraft", "1.0.15-6", nextDigest},
		"latest as a tag":        {"valheim", "latest", nextDigest},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := movePin(before, tc.name, tc.tag, tc.digest); err == nil {
				t.Errorf("movePin accepted %+v", tc)
			}
		})
	}
}

func TestDeliverWritesTheFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "releases.yaml")
	if err := os.WriteFile(p, []byte(realReleases(t)), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := deliver([]string{"-releases", p, "-name", "valheim", "-tag", "9.9.9-1", "-digest", nextDigest}); code != 0 {
		t.Fatalf("deliver exited %d", code)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `tag: "9.9.9-1"`) {
		t.Errorf("the file was not moved:\n%s", b)
	}
	if code := deliver([]string{"-releases", p, "-name", "valheim"}); code != 2 {
		t.Errorf("deliver without a tag and digest exited %d, want 2", code)
	}
}
