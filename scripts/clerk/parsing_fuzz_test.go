package main

import (
	"strings"
	"testing"
)

// Everything the clerk reads comes from outside it.
//
// A model's answer is arbitrary text however firmly the prompt asks for JSON,
// and a unified diff arrives from the GitHub API. Both are parsed by hand here,
// and both feed things that matter: what gets uploaded as a security alert, and
// whether a finding is judged visible on the pull request. A parser that panics
// takes the whole reading down; one that returns nonsense reports nonsense.

func FuzzUnfence(f *testing.F) {
	for _, seed := range []string{
		"{\"a\":1}",
		"```json\n{\"a\":1}\n```",
		"here you go:\n```\n{}\n```\nhope that helps",
		"", "```", "``", "```\n", "```json", "```a```b```",
		"\n\n  ```  \n x \n ```  \n\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := unfence(in)

		// It only ever trims and slices, so it cannot invent text.
		if len(out) > len(in) {
			t.Errorf("unfence grew %q from %d bytes to %d", in, len(in), len(out))
		}
		// It ends with a TrimSpace, so the result is already trimmed - a
		// caller that trims again is doing nothing, and one that does not
		// should be safe.
		if out != strings.TrimSpace(out) {
			t.Errorf("unfence returned %q, which is not trimmed, from %q", out, in)
		}
	})
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		`[{"rule":"unsound-work","path":"a.go","line":1,"message":"x"}]`,
		"```json\n[]\n```",
		"[]", "", "{", "null", `[{"line":-1}]`,
		`[{"rule":"x","path":"p","line":99999999999999999999,"message":"m"}]`,
		`{"findings":[]}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		found, err := parse(in)
		if err != nil {
			if len(found) != 0 {
				t.Errorf("parse failed on %q and still returned %d finding(s); a caller checking only the error would upload them", in, len(found))
			}
			return
		}
		// Anything that parses is about to become a SARIF result, so it has to
		// be something a reader can be sent to. keep() enforces the same rules
		// afterwards; this is about parse never producing a shape keep cannot
		// judge.
		for _, s := range found {
			if strings.ContainsAny(s.Path, "\x00") {
				t.Errorf("parse accepted a path containing a NUL from %q", in)
			}
		}
	})
}

func FuzzLinesFromPatch(f *testing.F) {
	for _, seed := range []string{
		"@@ -1,2 +1,2 @@\n a\n b",
		"@@ -5 +7 @@\n-old\n+new",
		"@@ -10,3 +12,4 @@ func x() {\n context\n+added",
		"", "@@", "@@ -0,0 +0,0 @@", "@@ -0,0 +0,1 @@",
		"@@ -1 +99999999999999999999 @@",
		" context line mentioning @@ -1 +1 @@ inside it",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, patch string) {
		lines := linesFromPatch(patch)
		for line := range lines {
			// A finding is anchored to a line number, and there is no line 0
			// in a file. Marking one shown says a finding there would render,
			// which is a claim about somewhere that does not exist.
			if line < 1 {
				t.Errorf("linesFromPatch reported line %d as shown, from %q; files start at line 1", line, patch)
			}
		}
	})
}
