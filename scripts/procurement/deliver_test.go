package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/details/applications"
	"homelab/details/repopath"
)

const (
	nextDigest  = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	otherDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

// Every site's applications file, comments and all, and the name of every
// release source in it - found rather than named, so the edit is proved
// against the files it will actually be run on, whichever sites those are
// and whatever they run.
func realSiteFiles(t *testing.T) map[string][]string {
	t.Helper()
	root, err := repopath.Root()
	if err != nil {
		t.Fatal(err)
	}
	found, err := filepath.Glob(filepath.Join(root, applications.SitesDir, "*", applications.SiteFile))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, f := range found {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, doc := range strings.Split(string(b), "\n---") {
			if m := metadataName.FindStringSubmatch(doc); m != nil && kindOCI.MatchString(doc) && !holdsItsPin.MatchString(doc) {
				out[string(b)] = append(out[string(b)], m[1])
			}
		}
	}
	return out
}

func source(name, tag, digest, annotations string) string {
	return `---
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: ` + name + `
  namespace: flux-system` + annotations + `
spec:
  ref:
    tag: "` + tag + `"
    digest: "` + digest + `"
`
}

// Exactly two lines change - the named source's tag and digest - so the pull
// request a person reviews is the release and nothing else. Held of every
// release every site pins today, and of a file written here so that it is
// still held of something when no site pins any.
func TestDeliverMovesOnlyThePinsTwoLines(t *testing.T) {
	files := realSiteFiles(t)
	files["# a comment\n"+source("thing", "1.0.0-1", otherDigest, "")+"# and one after\n"] = []string{"thing"}
	for before, names := range files {
		for _, name := range names {
			after, outcome, err := movePin(before, name, "9.9.9-1", nextDigest)
			if err != nil || outcome != pinMoved {
				t.Fatalf("%s: movePin = %v, err %v", name, outcome, err)
			}
			b, a := strings.Split(before, "\n"), strings.Split(after, "\n")
			if len(b) != len(a) {
				t.Fatalf("%s: the file went from %d lines to %d; only two lines may change", name, len(b), len(a))
			}
			var diff []string
			for i := range b {
				if b[i] != a[i] {
					diff = append(diff, a[i])
				}
			}
			if len(diff) != 2 || !strings.Contains(diff[0]+diff[1], `"9.9.9-1"`) || !strings.Contains(diff[0]+diff[1], nextDigest) {
				t.Errorf("%s: want exactly the tag and digest lines changed, got %q", name, diff)
			}

			again, outcome, err := movePin(after, name, "9.9.9-1", nextDigest)
			if err != nil || outcome != alreadyThere || again != after {
				t.Errorf("%s: delivering the pinned release again reported %v (%v)", name, outcome, err)
			}
		}
	}
}

func TestDeliverMovesOnlyTheNamedApplication(t *testing.T) {
	two := source("other", "1.0.0-1", otherDigest, "") + source("thing", "1.0.15-5", otherDigest, "")
	after, outcome, err := movePin(two, "thing", "1.0.15-6", nextDigest)
	if err != nil || outcome != pinMoved {
		t.Fatal(outcome, err)
	}
	if !strings.Contains(after, `tag: "1.0.0-1"`) || strings.Count(after, otherDigest) != 1 {
		t.Errorf("moving one application also moved another's pin:\n%s", after)
	}
	if !strings.Contains(after, `tag: "1.0.15-6"`) {
		t.Errorf("the pin did not move:\n%s", after)
	}
}

// A site that does not run the application, and one that holds its pin, are
// left exactly as they were: that is how two sites run two versions.
func TestDeliverLeavesASiteThatDoesNotFollowAlone(t *testing.T) {
	for name, c := range map[string]struct {
		body string
		want delivery
	}{
		"not run here":            {source("other", "1.0.0-1", otherDigest, ""), notRun},
		"an empty file":           {"", notRun},
		"holding its pin":         {source("thing", "1.0.0-1", otherDigest, "\n  annotations:\n    homelab/delivery: hold"), held},
		"holding its pin, quoted": {source("thing", "1.0.0-1", otherDigest, "\n  annotations:\n    homelab/delivery: \"hold\""), held},
	} {
		after, outcome, err := movePin(c.body, "thing", "1.0.15-6", nextDigest)
		if err != nil || outcome != c.want || after != c.body {
			t.Errorf("%s: outcome %v, err %v, changed %v", name, outcome, err, after != c.body)
		}
	}
	// Saying it follows, or saying nothing, is following.
	follows := source("thing", "1.0.0-1", otherDigest, "\n  annotations:\n    homelab/delivery: follow")
	if _, outcome, err := movePin(follows, "thing", "1.0.15-6", nextDigest); err != nil || outcome != pinMoved {
		t.Errorf("a source that follows was not moved: %v, %v", outcome, err)
	}
}

// Nothing that is not a release reaches the file: a pin that is not a digest
// is a pin a registry push can move. And a file this cannot edit safely is
// refused rather than guessed at.
func TestDeliverRefusesWhatIsNotARelease(t *testing.T) {
	one := source("thing", "1.0.0-1", otherDigest, "")
	for name, tc := range map[string]struct{ body, tag, digest string }{
		"a tag with no counter":  {one, "1.0.15", nextDigest},
		"a digest that is short": {one, "1.0.15-6", "sha256:abc"},
		"latest as a tag":        {one, "latest", nextDigest},
		"two sources of a name":  {one + one, "1.0.15-6", nextDigest},
		"a source with no pin":   {"---\nkind: OCIRepository\nmetadata:\n  name: thing\nspec:\n  ref:\n    tag: \"1.0.0-1\"\n", "1.0.15-6", nextDigest},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := movePin(tc.body, "thing", tc.tag, tc.digest); err == nil {
				t.Errorf("movePin accepted %+v", tc)
			}
		})
	}
}

func TestDeliverWritesTheFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, applications.SiteFile)
	before := source("thing", "1.0.0-1", otherDigest, "")
	if err := os.WriteFile(p, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := deliver([]string{"-releases", p, "-name", "absent", "-tag", "9.9.9-1", "-digest", nextDigest}); code != 0 {
		t.Fatalf("deliver of an application the site does not run exited %d", code)
	}
	if b, _ := os.ReadFile(p); string(b) != before {
		t.Errorf("a delivery of an application the site does not run changed the file:\n%s", b)
	}
	if code := deliver([]string{"-releases", p, "-name", "thing", "-tag", "9.9.9-1", "-digest", nextDigest}); code != 0 {
		t.Fatalf("deliver exited %d", code)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `tag: "9.9.9-1"`) {
		t.Errorf("the file was not moved:\n%s", b)
	}
	if code := deliver([]string{"-releases", p, "-name", "thing"}); code != 2 {
		t.Errorf("deliver without a tag and digest exited %d, want 2", code)
	}
	if code := deliver([]string{"-releases", p, "-name", "thing", "-tag", "latest", "-digest", nextDigest}); code != 1 {
		t.Errorf("deliver of what is not a release exited %d, want 1", code)
	}
	if code := deliver([]string{"-releases", filepath.Join(dir, "absent.yaml"), "-name", "thing", "-tag", "9.9.9-1", "-digest", nextDigest}); code != 1 {
		t.Errorf("deliver into a file that is not there exited %d, want 1", code)
	}
}
