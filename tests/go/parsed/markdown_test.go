package parsed

import (
	"slices"
	"testing"
)

// Prose, code spans and code blocks come apart, each with its line, and the
// fence of a block is not its text.
func TestMarkdownTellsProseFromCode(t *testing.T) {
	src := "# Title\n\nSome `inline` prose.\n\n```sh\nmake all\n```\n"
	got := MarkdownOf([]byte(src))
	want := []MarkdownPart{
		{MarkdownText, "Title", 1},
		{MarkdownText, "Some ", 3},
		{MarkdownCodeSpan, "inline", 3},
		{MarkdownText, " prose.", 3},
		{MarkdownCodeBlock, "make all\n", 6},
	}
	if !slices.Equal(got, want) {
		t.Errorf("MarkdownOf read\n  %+v\nwant\n  %+v", got, want)
	}
}

// A table comes back header first, a row at a time, with bold kept.
func TestMarkdownTablesKeepRowsCellsAndBold(t *testing.T) {
	src := "Intro.\n\n| Tier | Runs |\n| ---- | ---- |\n| **unit** | `go test` |\n| plain | x |\n"
	got := MarkdownTablesOf([]byte(src))
	want := [][][]string{{{"Tier", "Runs"}, {"**unit**", "`go test`"}, {"plain", "x"}}}
	if len(got) != 1 || len(got[0]) != 3 {
		t.Fatalf("read %v, want %v", got, want)
	}
	for i := range want[0] {
		if !slices.Equal(got[0][i], want[0][i]) {
			t.Errorf("row %d is %q, want %q", i, got[0][i], want[0][i])
		}
	}
}
