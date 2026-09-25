package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	"homelab/details/release"
)

// deliver brings a finished release to the estate's gate.
//
// The fabricator builds and publishes; it never writes to the repository.
// Procurement fetched the order, and it is procurement that brings the
// finished work back: this moves one workload's pin in the production
// releases file to the release just published, and the workflow around it
// opens the pull request. Merging that pull request is letting the delivery
// in, and that stays a person's decision - a delivery is never merged without
// review (superintendent enforce-standing-order).
//
// This verb only edits the file. It is text in and text out so it can be
// tested without a registry or GitHub, and it changes exactly two lines - the
// named source's tag and digest - so the diff a person reviews is the release
// and nothing else.
func deliver(args []string) int {
	fs := flag.NewFlagSet("deliver", flag.ContinueOnError)
	file := fs.String("releases", "", "the production releases file to move a pin in")
	name := fs.String("name", "", "the workload whose pin moves: an OCIRepository's metadata.name")
	tag := fs.String("tag", "", "the release's tag, e.g. 1.0.15-6")
	digest := fs.String("digest", "", "the release's digest, sha256:<64 hex>")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || *name == "" || *tag == "" || *digest == "" {
		fmt.Fprintln(os.Stderr, "procurement deliver: -releases, -name, -tag and -digest are all required")
		return 2
	}
	body, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "procurement deliver:", err)
		return 1
	}
	moved, changed, err := movePin(string(body), *name, *tag, *digest)
	if err != nil {
		fmt.Fprintln(os.Stderr, "procurement deliver:", err)
		return 1
	}
	if !changed {
		fmt.Printf("%s is already at %s\n", *name, *tag)
		return 0
	}
	if err := os.WriteFile(*file, []byte(moved), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "procurement deliver:", err)
		return 1
	}
	fmt.Printf("moved %s to %s\n", *name, *tag)
	return 0
}

var (
	pinTag    = regexp.MustCompile(`^(\s+tag:\s*)"[^"]*"(\s*)$`)
	pinDigest = regexp.MustCompile(`^(\s+digest:\s*)"[^"]*"(\s*)$`)
)

// movePin rewrites the tag and digest of the OCIRepository called name, and
// nothing else. changed is false when it already pins that release.
func movePin(body, name, tag, digest string) (string, bool, error) {
	if !release.Tag.MatchString(tag) {
		return "", false, fmt.Errorf("%q is not a release tag (version-counter, e.g. 1.0.15-6)", tag)
	}
	if !release.Digest.MatchString(digest) {
		return "", false, fmt.Errorf("%q is not a sha256 digest", digest)
	}

	docs := strings.Split(body, "\n---")
	found := false
	changed := false
	for i, doc := range docs {
		if !isSourceNamed(doc, name) {
			continue
		}
		if found {
			return "", false, fmt.Errorf("two OCIRepositories are called %s, so which pin to move is ambiguous", name)
		}
		found = true
		lines := strings.Split(doc, "\n")
		sawTag, sawDigest := false, false
		for j, line := range lines {
			if m := pinTag.FindStringSubmatch(line); m != nil {
				next := m[1] + `"` + tag + `"` + m[2]
				changed = changed || next != line
				lines[j], sawTag = next, true
			}
			if m := pinDigest.FindStringSubmatch(line); m != nil {
				next := m[1] + `"` + digest + `"` + m[2]
				changed = changed || next != line
				lines[j], sawDigest = next, true
			}
		}
		if !sawTag || !sawDigest {
			return "", false, fmt.Errorf("the source %s does not pin both a tag and a digest, so there is nothing to move", name)
		}
		docs[i] = strings.Join(lines, "\n")
	}
	if !found {
		return "", false, errors.New("no OCIRepository called " + name + " in the releases file")
	}
	return strings.Join(docs, "\n---"), changed, nil
}

var (
	kindOCI      = regexp.MustCompile(`(?m)^kind:\s*OCIRepository\s*$`)
	metadataName = regexp.MustCompile(`(?m)^metadata:\s*\n(?:\s+.*\n)*?\s{2}name:\s*(\S+)\s*$`)
)

func isSourceNamed(doc, name string) bool {
	if !kindOCI.MatchString(doc) {
		return false
	}
	m := metadataName.FindStringSubmatch(doc)
	return m != nil && m[1] == name
}
