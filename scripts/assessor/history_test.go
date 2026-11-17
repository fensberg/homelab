package main

import (
	"reflect"
	"testing"
)

// The required list is read from OpenTofu with its comments left out, and
// a source without one has none.
func TestTheRequiredChecksAreReadFromTheirDeclaration(t *testing.T) {
	source := "x = 1\nrequired_checks = [\n  # \"not this\"\n  \"one\",\n  \"two\", \"three\",\n]\ny = [\"after\"]\n"
	if got := requiredIn(source); !reflect.DeepEqual(got, []string{"one", "two", "three"}) {
		t.Errorf("got %v", got)
	}
	if got := requiredIn("nothing here"); got != nil {
		t.Errorf("got %v from a source with no list", got)
	}
}

// Each issue a comment names is read once, in order, and only a test whose
// own comment cites one is a guard.
func TestAGuardIsATestWhoseCommentCitesAnIssue(t *testing.T) {
	if got := cited("see #12, then #3 and #12 again; not a#4b or #x"); !reflect.DeepEqual(got, []int{12, 3}) {
		t.Errorf("got %v", got)
	}
	source := "// Cites #7.\nfunc TestCites(t *testing.T) {}\n\n// Cites nothing.\nfunc TestQuiet(t *testing.T) {}\n\nfunc TestBare(t *testing.T) {}\n"
	want := []guard{{Kind: "Go test", Name: "TestCites", File: "g_test.go", Issues: []int{7}}}
	if got := goGuardsIn("g_test.go", source); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

// The history gives each required set from the day it landed, each guard
// and rule citing an issue with the day it landed, and each edit to a file.
func TestTheHistoryIsReadFromGit(t *testing.T) {
	h := history{smallHistory(t), "HEAD"}

	reqs, err := h.requirements()
	if err != nil || len(reqs) != 1 || !reqs[0].From.Equal(at("2026-01-10T00:00:00Z")) ||
		!reflect.DeepEqual(reqs[0].Checks, []string{"tests passed", "Gate"}) {
		t.Errorf("got %+v, %v", reqs, err)
	}

	guards, err := h.guards("guards", "rules")
	want := []guard{
		{Kind: "Go test", Name: "TestNothingTwice", File: "guards/a_test.go", Issues: []int{12}, Added: at("2026-01-01T00:00:00Z")},
		{Kind: "Conftest rule", Name: "kept", File: "rules/kept/kept.rego", Issues: []int{13, 99}, Added: at("2026-01-01T00:00:00Z")},
	}
	if err != nil || len(guards) != 2 {
		t.Fatalf("got %+v, %v", guards, err)
	}
	for i := range want {
		if !reflect.DeepEqual(guards[i].Issues, want[i].Issues) || guards[i].Name != want[i].Name ||
			guards[i].File != want[i].File || !guards[i].Added.Equal(want[i].Added) {
			t.Errorf("guard %d: got %+v, want %+v", i, guards[i], want[i])
		}
	}

	edits, err := h.edits("GUIDE.md")
	if err != nil || len(edits) != 3 {
		t.Errorf("got %v, %v; want three edits", edits, err)
	}

	gone := history{t.TempDir(), "HEAD"}
	if _, err := gone.requirements(); err == nil {
		t.Error("a directory that is not a repository should be an error")
	}
	if _, err := gone.guards("guards", "rules"); err == nil {
		t.Error("a directory that is not a repository should be an error")
	}
	if _, err := gone.edits("GUIDE.md"); err == nil {
		t.Error("a directory that is not a repository should be an error")
	}
	if _, err := times("not-a-date"); err == nil {
		t.Error("a date that is not one should be an error")
	}
}
