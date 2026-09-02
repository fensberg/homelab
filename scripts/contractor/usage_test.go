package main

import (
	"regexp"
	"strings"
	"testing"
)

// The help must list the verbs that exist.
//
// It did not. `knownVerbs` carried break-ground and demolish while the usage
// text still offered `ignite` and `destroy`, so anyone following the program's
// own help got "unknown verb" from the program itself. That survived a rename
// because nothing compared the two lists, and both are hand-maintained.
//
// This is the cheap half of a naming change: the expensive half is the rename
// itself, and the cheap half is making the drift impossible afterwards.
func TestTheHelpListsExactlyTheVerbsThatExist(t *testing.T) {
	// Verb lines in the usage block are "  <verb>  <description>", indented
	// two spaces, with the verb at the start.
	// Exactly two leading spaces, then the verb. Continuation lines in a
	// description are indented far further, so they cannot match.
	verbLine := regexp.MustCompile(`(?m)^ {2}([a-z][a-z-]+)\s+\S`)

	listed := map[string]bool{}
	for _, m := range verbLine.FindAllStringSubmatch(usage, -1) {
		listed[m[1]] = true
	}
	if len(listed) == 0 {
		t.Fatal("no verbs could be read out of the usage text, so this proves nothing")
	}

	for _, v := range knownVerbs {
		if !listed[v] {
			t.Errorf("%q is a verb the program accepts and the help does not mention it.\n"+
				"Somebody reading `contractor -h` cannot discover it.", v)
		}
	}
	for v := range listed {
		if !slicesContains(knownVerbs, v) {
			t.Errorf("the help offers %q, which the program rejects as an unknown verb.\n"+
				"Following the program's own documentation produces an error - which is "+
				"how `ignite` and `destroy` outlived the rename that replaced them.", v)
		}
	}
}

func slicesContains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// The package comment should not name a command that does not exist either.
func TestNoRetiredVerbSurvivesInTheHelp(t *testing.T) {
	for _, retired := range []string{"ignite", "destroy"} {
		if strings.Contains(usage, "  "+retired+" ") {
			t.Errorf("the usage text still offers the retired verb %q", retired)
		}
	}
}
