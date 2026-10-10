package main

import (
	"fmt"
	"regexp"
	"strings"

	"homelab/details/drafter"
)

// Checking a finding before the operator has to.
//
// WHERE THIS CAME FROM. The clerk's first substantive finding was wrong (#233):
//
//	clerk / The work is unsound - Duplicate key 'ref' is specified inside the
//	upload-sarif step.
//
// There are three `ref:` keys in that file. One belongs to actions/checkout and
// two belong to the two upload-sarif steps, so each is in a different step's
// `with:` mapping and none duplicates another. The model saw three identical
// keys at the same indentation and reported a duplicate without tracking which
// mapping each belonged to. It read the file; it did not parse it.
//
// THE RULE THIS EXTENDS. `keep` already drops a finding that cannot be checked
// in ten seconds - one naming no file, or a file that was never read, or a line
// past the end of it. That rule is about whether a claim is CHECKABLE. This is
// the next step: where a claim is checkable MECHANICALLY, check it, and drop it
// if it is false. Every finding removed here is one the operator would
// otherwise have spent time disproving.
//
// WHY THIS CLASS AND NOT MORE. It is narrow by nature and that is the point. A
// claim about whether code is well structured is a judgement and stays one; a
// claim that a YAML file contains a duplicate key is a fact, settled by a
// parser in milliseconds. Only claims of the second kind belong here, and a
// check that is not exact does not belong at all - dropping a TRUE finding is a
// far worse failure than showing a false one, so anything this cannot settle
// with certainty it leaves alone.

// A claim that a structured file contains a duplicate key.
var duplicateKeyClaim = regexp.MustCompile(`(?i)\bduplicat(?:e|ed|ion)\b[^.]{0,80}?\bkey`)

// A quoted key name, so the claim can be checked against the key it names.
var quotedName = regexp.MustCompile(`['"` + "`" + `]([A-Za-z0-9_.-]+)['"` + "`" + `]`)

// falsified reports whether a finding makes a mechanically checkable claim that
// is false, and why.
//
// It returns false for everything it cannot settle. "I could not check this"
// and "this is false" must never be the same answer, because only one of them
// is a reason to throw a finding away.
func falsified(s snag, body string) (string, bool) {
	if body == "" {
		return "", false
	}
	if !duplicateKeyClaim.MatchString(s.Message) {
		return "", false
	}
	if !isYAML(s.Path) {
		return "", false
	}

	dupes, read := duplicateKeys(body)
	if !read {
		return "", false
	}
	if len(dupes) == 0 {
		return fmt.Sprintf("claims a duplicate key in %s, and there is none", s.Path), true
	}

	// The file does contain a duplicate somewhere. If the finding named a key,
	// and that key is not one of them, the claim is still false.
	if m := quotedName.FindStringSubmatch(s.Message); m != nil {
		for _, d := range dupes {
			if d == m[1] {
				return "", false
			}
		}
		return fmt.Sprintf("claims %q is a duplicate key in %s; the duplicates there are %s",
			m[1], s.Path, strings.Join(dupes, ", ")), true
	}
	return "", false
}

func isYAML(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".yaml")
}

// duplicateKeys returns the names that appear twice in one mapping, and
// whether the file could be read at all.
//
// "Settled by a parser in milliseconds" is what the note above says of this
// claim, and for as long as this program took no dependency it was settled
// by something else: indentation tracked line by line, with block scalars
// skipped by pattern. The drafter reads the file as YAML and names the keys.
// Two keys with the same name are duplicates only when they are in the same
// mapping; three `ref:` keys in three `with:` blocks are three mappings.
//
// A file that cannot be read settles nothing. That is said, and is never an
// answer of "none": only one of the two is a reason to throw a finding away.
func duplicateKeys(body string) ([]string, bool) {
	names, err := drafter.Duplicates([]byte(body))
	if err != nil {
		return nil, false
	}
	return names, true
}
