package repo

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"homelab/details/release"
)

// Every release a site runs is pinned by digest.
//
// A release is published under a tag, and a tag in a registry is a pointer
// anyone who can push may move. Pinned by tag alone, the bytes a site runs
// could change with no pull request, no review and nothing in git recording
// it - which is the property a site's block exists to give it. So every
// OCIRepository under clusters/ names a digest, and the tag beside it is for
// the people reading the diff.
//
// Found by walking clusters/, so a source added anywhere Flux reads is held to
// it without this test being told.
func TestEveryReleaseIsPinnedByDigest(t *testing.T) {
	root := repoRoot(t)
	// No floor on how many there are: a site that runs no application pins
	// no release. What this refuses is proved against sources written here,
	// in TestUnpinnedReleasesNamesEachWayAReleaseIsNotPinned.
	for _, rel := range trackedMatching(t, func(p string) bool {
		return strings.HasPrefix(p, fluxTree+"/") && (strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml"))
	}) {
		if strings.HasSuffix(rel, "gotk-components.yaml") {
			continue // Flux's own CRDs, vendored verbatim
		}
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		problems, err := unpinnedReleases(rel, body)
		if err != nil {
			t.Fatal(err)
		}
		for _, problem := range problems {
			t.Error(problem)
		}
	}
}

// unpinnedReleases is every release source in one manifest that is not pinned
// by digest, or pinned with no tag for a person to read.
func unpinnedReleases(rel string, body []byte) ([]string, error) {
	var problems []string
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
			return problems, nil
		}
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", rel, err)
		}
		if doc.Kind != "OCIRepository" {
			continue
		}
		if !release.Digest.MatchString(doc.Spec.Ref.Digest) {
			problems = append(problems, fmt.Sprintf("%s: the release %q is not pinned by digest (ref.digest is %q).\n\n"+
				"A tag can be moved in the registry without a pull request, so the site "+
				"would run whatever it points at today. Pin the digest the fabricator "+
				"reported for this release.", rel, doc.Metadata.Name, doc.Spec.Ref.Digest))
		}
		if doc.Spec.Ref.Tag == "" {
			problems = append(problems, fmt.Sprintf("%s: the release %q has no tag, so a diff moving its digest says "+
				"nothing a person can read. Name the version beside the digest.", rel, doc.Metadata.Name))
		}
	}
}

// The check is held to what it claims, against sources written here: one
// pinned by tag and digest is accepted, and each way of not pinning is named.
func TestUnpinnedReleasesNamesEachWayAReleaseIsNotPinned(t *testing.T) {
	source := func(ref string) []byte {
		return []byte("kind: ConfigMap\nmetadata: {name: other}\n---\nkind: OCIRepository\nmetadata: {name: thing}\nspec:\n  ref:\n" + ref)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	for name, c := range map[string]struct {
		ref  string
		want []string
	}{
		"a tag and a digest": {"    tag: \"1.0.0-1\"\n    digest: \"" + digest + "\"\n", nil},
		"a tag alone":        {"    tag: \"1.0.0-1\"\n", []string{"is not pinned by digest"}},
		"a digest alone":     {"    digest: \"" + digest + "\"\n", []string{"has no tag"}},
		"a digest cut short": {"    tag: \"1.0.0-1\"\n    digest: \"sha256:abc\"\n", []string{"is not pinned by digest"}},
		"nothing pinned":     {"    semver: \"*\"\n", []string{"is not pinned by digest", "has no tag"}},
	} {
		got, err := unpinnedReleases("f.yaml", source(c.ref))
		if err != nil || len(got) != len(c.want) {
			t.Errorf("%s: want %d problem(s), got %v (%v)", name, len(c.want), got, err)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(strings.Join(got, "\n"), w) {
				t.Errorf("%s: no problem says %q: %v", name, w, got)
			}
		}
	}
	if _, err := unpinnedReleases("f.yaml", []byte("kind: [\n")); err == nil {
		t.Error("a manifest that does not parse was read as pinning everything")
	}
}
