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
