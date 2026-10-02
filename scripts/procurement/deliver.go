package main

import (
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
// finished work back: this moves one application's pin, in one site's file,
// to the release just published, and the workflow around it opens the pull
// request. A site that does not run the application is left alone, and so is
// one whose source says homelab/delivery: hold - which is how a
// site stays on the release it pins while another moves. Merging that pull request is letting the delivery
// in, and that stays a person's decision - a delivery is never merged without
// review (superintendent enforce-standing-order).
//
// This verb only edits the file. It is text in and text out so it can be
// tested without a registry or GitHub, and it changes exactly two lines - the
// named source's tag and digest - so the diff a person reviews is the release
// and nothing else.
func deliver(args []string) int {
	fs := flag.NewFlagSet("deliver", flag.ContinueOnError)
	file := fs.String("releases", "", "the site's applications file to move a pin in")
	name := fs.String("name", "", "the application whose pin moves: an OCIRepository's metadata.name")
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
	moved, outcome, err := movePin(string(body), *name, *tag, *digest)
	if err != nil {
		fmt.Fprintln(os.Stderr, "procurement deliver:", err)
		return 1
	}
	switch outcome {
	case notRun:
		fmt.Printf("%s is not run here\n", *name)
	case held:
		fmt.Printf("%s holds its pin here, so it is not moved\n", *name)
	case alreadyThere:
		fmt.Printf("%s is already at %s\n", *name, *tag)
	case pinMoved:
		if err := os.WriteFile(*file, []byte(moved), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "procurement deliver:", err)
			return 1
		}
		fmt.Printf("moved %s to %s\n", *name, *tag)
	}
	return 0
}

// What a delivery to one site's file came to.
type delivery int

const (
	notRun       delivery = iota // the site has no source of that name
	held                         // the source holds its pin
	alreadyThere                 // the source already pins this release
	pinMoved                     // the pin moved; the text changed
)

// holdsItsPin is the annotation by which a site keeps the release it pins.
var holdsItsPin = regexp.MustCompile(`(?m)^\s+homelab/delivery:\s*"?hold"?\s*$`)

var (
	pinTag    = regexp.MustCompile(`^(\s+tag:\s*)"[^"]*"(\s*)$`)
	pinDigest = regexp.MustCompile(`^(\s+digest:\s*)"[^"]*"(\s*)$`)
)

// movePin rewrites the tag and digest of the OCIRepository called name, and
// nothing else. The text comes back unchanged unless the outcome is pinMoved.
func movePin(body, name, tag, digest string) (string, delivery, error) {
	if !release.Tag.MatchString(tag) {
		return "", notRun, fmt.Errorf("%q is not a release tag (version-counter, e.g. 1.0.15-6)", tag)
	}
	if !release.Digest.MatchString(digest) {
		return "", notRun, fmt.Errorf("%q is not a sha256 digest", digest)
	}

	docs := strings.Split(body, "\n---")
	found := false
	outcome := notRun
	for i, doc := range docs {
		if !isSourceNamed(doc, name) {
			continue
		}
		if found {
			return "", notRun, fmt.Errorf("two OCIRepositories are called %s, so which pin to move is ambiguous", name)
		}
		found = true
		if holdsItsPin.MatchString(doc) {
			outcome = held
			continue
		}
		outcome = alreadyThere
		lines := strings.Split(doc, "\n")
		sawTag, sawDigest := false, false
		for j, line := range lines {
			if m := pinTag.FindStringSubmatch(line); m != nil {
				next := m[1] + `"` + tag + `"` + m[2]
				if next != line {
					outcome = pinMoved
				}
				lines[j], sawTag = next, true
			}
			if m := pinDigest.FindStringSubmatch(line); m != nil {
				next := m[1] + `"` + digest + `"` + m[2]
				if next != line {
					outcome = pinMoved
				}
				lines[j], sawDigest = next, true
			}
		}
		if !sawTag || !sawDigest {
			return "", notRun, fmt.Errorf("the source %s does not pin both a tag and a digest, so there is nothing to move", name)
		}
		docs[i] = strings.Join(lines, "\n")
	}
	if outcome != pinMoved {
		return body, outcome, nil
	}
	return strings.Join(docs, "\n---"), pinMoved, nil
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
