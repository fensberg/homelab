package parsed

import "testing"

// The parser is exercised on its own, because CI reimplements it in shell and
// the two must agree about what a line means.
func TestSensitivePathsOfReadsEachLineAsCIDoes(t *testing.T) {
	got := SensitivePathsOf(`
# a whole-line comment
   # an indented comment

path/one/     # first reason
path/two      #second reason, no space
path/three
`)
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(got), got)
	}
	if got[0].Path != "path/one/" || got[0].Why != "first reason" {
		t.Errorf("entry 0 = %+v", got[0])
	}
	if got[1].Why != "second reason, no space" {
		t.Errorf("a reason with no space after # should still parse: %q", got[1].Why)
	}
	if got[2].Path != "path/three" || got[2].Why != "" {
		t.Errorf("a path with no reason should parse with an empty reason: %+v", got[2])
	}
}
