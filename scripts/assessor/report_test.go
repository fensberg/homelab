package main

import (
	"strings"
	"testing"
)

// A table is padded as the formatter leaves one, counting characters and
// not bytes.
func TestATableIsWrittenAsTheFormatterLeavesIt(t *testing.T) {
	got := table([]string{"Name", "N"}, [][]string{{"❯❱ é", "10"}, {"a", "2"}})
	want := "| Name | N   |\n| ---- | --- |\n| ❯❱ é | 10  |\n| a    | 2   |\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// A share says when nothing was measured, and the top findings are the most
// frequent first, then by name.
func TestSharesAndTopFindingsAreWrittenPlainly(t *testing.T) {
	if got := share(tally{Unmeasured: 3}); got != "none measured" {
		t.Errorf("got %q", got)
	}
	if got := share(tally{Passed: 1, Failed: 3}); got != "1 of 4 (25.0%)" {
		t.Errorf("got %q", got)
	}
	rows := top(map[finding]int{
		{"Go test", "TestB"}: 2, {"Go test", "TestA"}: 2, {"Go test", "TestC"}: 5, {"Conftest rule", "r"}: 9,
	}, "Go test", 2)
	if len(rows) != 2 || rows[0][0] != "`TestC`" || rows[1][0] != "`TestA`" {
		t.Errorf("got %v", rows)
	}
}

// A report of nothing measured still says what it could not measure.
func TestAnEmptyReportSaysWhatItCouldNotMeasure(t *testing.T) {
	got := render(&report{Families: map[string]int{}, Findings: map[finding]int{}})
	for _, want := range []string{"none measured", "## Limits", "## Queries used"} {
		if !strings.Contains(got, want) {
			t.Errorf("an empty report does not say %q", want)
		}
	}
}
