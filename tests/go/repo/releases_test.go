package repo

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"homelab/details/release"
)

// Every release production runs is pinned by digest.
//
// A release is published under a tag, and a tag in a registry is a pointer
// anyone who can push may move. Pinned by tag alone, the bytes production runs
// could change with no pull request, no review and nothing in git recording
// it - which is the property releases.yaml exists to give production. So every
// OCIRepository under clusters/ names a digest, and the tag beside it is for
// the people reading the diff.
//
// Found by walking clusters/, so a source added anywhere Flux reads is held to
// it without this test being told.
func TestEveryReleaseIsPinnedByDigest(t *testing.T) {
	root := repoRoot(t)
	found := 0
	for _, rel := range trackedMatching(t, func(p string) bool {
		return strings.HasPrefix(p, "clusters/") && (strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml"))
	}) {
		if strings.HasSuffix(rel, "gotk-components.yaml") {
			continue // Flux's own CRDs, vendored verbatim
		}
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		dec := yaml.NewDecoder(bytes.NewReader(body))
		for {
			var doc struct {
				Kind     string `yaml:"kind"`
				Metadata struct {
					Name string `yaml:"name"`
				} `yaml:"metadata"`
				Spec struct {
					Ref struct {
						Tag    string `yaml:"tag"`
						Digest string `yaml:"digest"`
					} `yaml:"ref"`
				} `yaml:"spec"`
			}
			err := dec.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("parsing %s: %v", rel, err)
			}
			if doc.Kind != "OCIRepository" {
				continue
			}
			found++
			if !release.Digest.MatchString(doc.Spec.Ref.Digest) {
				t.Errorf("%s: the release %q is not pinned by digest (ref.digest is %q).\n\n"+
					"A tag can be moved in the registry without a pull request, so production "+
					"would run whatever it points at today. Pin the digest the fabricator "+
					"reported for this release.", rel, doc.Metadata.Name, doc.Spec.Ref.Digest)
			}
			if doc.Spec.Ref.Tag == "" {
				t.Errorf("%s: the release %q has no tag, so a diff moving its digest says "+
					"nothing a person can read. Name the version beside the digest.", rel, doc.Metadata.Name)
			}
		}
	}
	if found == 0 {
		t.Fatal("no OCIRepository was found under clusters/, so production pins no release " +
			"and this checked nothing")
	}
}
