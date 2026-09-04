package main

import (
	"strings"
	"testing"
)

const lyingGo = `package thing

import "fmt"

// Retries three times before giving up, with a backoff between attempts.
func try() {
	fmt.Println("once")
}

// The separator is a slash, as in http://example.com/path.
const sep = "//"
`

// The blind pass must not be told what the code is supposed to do.
func TestGoCommentsAreTakenAwayFromTheCode(t *testing.T) {
	code, prose, ok := split("thing.go", lyingGo)
	if !ok {
		t.Fatal("Go should split")
	}
	if strings.Contains(code, "Retries three times") {
		t.Error("the claim reached the blind pass")
	}
	if !strings.Contains(prose, "Retries three times") {
		t.Error("the claim was lost instead of set aside")
	}
	if !strings.Contains(code, `fmt.Println("once")`) {
		t.Errorf("the code did not survive:\n%s", code)
	}
}

// A citation is only checkable if the line numbers are the file's own.
//
// Deleting the commentary moves every line after it, so a finding citing line
// 40 of what the model read points at a different line 40 in the file the
// operator opens. Blanking keeps them the same.
func TestBlankingKeepsEveryLineNumberTrue(t *testing.T) {
	for _, c := range []struct{ name, path, body string }{
		{"go", "thing.go", lyingGo},
		{"yaml", "ci.yml", "# a claim\njobs:\n  build:\n    timeout-minutes: 10\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, _, _ := split(c.path, c.body)
			was := strings.Count(c.body, "\n")
			now := strings.Count(code, "\n")
			if was != now {
				t.Fatalf("the file had %d newlines and the blind pass sees %d", was, now)
			}
			for i, line := range strings.Split(c.body, "\n") {
				if strings.Contains(line, "timeout-minutes") || strings.Contains(line, `Println("once")`) {
					got := strings.Split(code, "\n")[i]
					if strings.TrimSpace(got) != strings.TrimSpace(line) {
						t.Errorf("line %d moved: was %q, now %q", i+1, line, got)
					}
				}
			}
		})
	}
}

// Parsing rather than pattern-matching, and this is why.
//
// A regular expression over "//" removes the slashes inside the URL and inside
// the string literal, corrupting the code the blind pass has to reason about -
// and then reports a finding about the corruption.
func TestASlashInsideAStringIsNotACommentMarker(t *testing.T) {
	code, _, _ := split("thing.go", lyingGo)
	if !strings.Contains(code, `"//"`) {
		t.Errorf("the string literal was mangled as if it were a comment:\n%s", code)
	}
}

func TestHashCommentsAreTakenFromYAML(t *testing.T) {
	const y = "# every job is bounded\njobs:\n  build:\n    timeout-minutes: 10\n"
	code, prose, ok := split("ci.yml", y)
	if !ok {
		t.Fatal("yaml should split")
	}
	if strings.Contains(code, "bounded") {
		t.Error("the claim reached the blind pass")
	}
	if !strings.Contains(prose, "bounded") || !strings.Contains(code, "timeout-minutes") {
		t.Errorf("split wrong:\ncode=%q\nprose=%q", code, prose)
	}
}

// A trailing marker is left alone on purpose.
//
// Taking it would mangle a URL, a colour or a string more often than it would
// find a real comment, and the blind pass reasons about the code.
func TestATrailingMarkerIsLeftAlone(t *testing.T) {
	code, _, _ := split("x.yml", "url: https://example.com/a#b\n")
	if !strings.Contains(code, "example.com/a#b") {
		t.Errorf("a URL was cut at a hash: %q", code)
	}
}

// A record is a claim about the estate, not a thing that runs.
func TestMarkdownIsAllProse(t *testing.T) {
	code, prose, ok := split("docs/epochs/01.md", "# Epoch\n\nThe button is idempotent.\n")
	if ok {
		t.Error("markdown should not be offered to the blind pass as code")
	}
	if code != "" || !strings.Contains(prose, "idempotent") {
		t.Errorf("code=%q prose=%q", code, prose)
	}
}

// Unparsable Go still gets read, with a weaker separation rather than none.
func TestBrokenGoFallsBackRatherThanRefusing(t *testing.T) {
	code, prose, ok := split("broken.go", "package x\n// a claim\nfunc (\n")
	if !ok {
		t.Fatal("should still split")
	}
	if strings.Contains(code, "a claim") || !strings.Contains(prose, "a claim") {
		t.Errorf("fallback did not separate:\ncode=%q prose=%q", code, prose)
	}
}
