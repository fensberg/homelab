package parsed

import (
	"bytes"
	"strings"
	"testing"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// MarkdownPart is one piece of a Markdown document as goldmark reads it:
// prose, a code span, a code block, or raw HTML, with the line it starts on.
type MarkdownPart struct {
	Kind string // "text", "code-span", "code-block" or "html"
	Text string
	Line int
}

// The kinds of a MarkdownPart.
const (
	MarkdownText      = "text"
	MarkdownCodeSpan  = "code-span"
	MarkdownCodeBlock = "code-block"
	MarkdownHTML      = "html"
)

// Markdown is a Markdown file of the tree, as goldmark parses it, in the
// order it is written. Prose is given a run of text at a time, so a phrase
// broken by emphasis or a link is two parts.
func (r *Tree) Markdown(t testing.TB, rel string) []MarkdownPart {
	t.Helper()
	return MarkdownOf(r.read(t, rel))
}

// MarkdownOf parses Markdown a parse handed over as a value.
func MarkdownOf(src []byte) []MarkdownPart {
	doc := goldmark.New().Parser().Parse(text.NewReader(src))
	line := func(offset int) int { return bytes.Count(src[:offset], []byte("\n")) + 1 }
	var out []MarkdownPart
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Text:
			out = append(out, MarkdownPart{MarkdownText, string(n.Segment.Value(src)), line(n.Segment.Start)})
		case *ast.CodeSpan:
			var b strings.Builder
			for c := n.FirstChild(); c != nil; c = c.NextSibling() {
				if t, ok := c.(*ast.Text); ok {
					b.Write(t.Segment.Value(src))
				}
			}
			start := 0
			if c, ok := n.FirstChild().(*ast.Text); ok {
				start = c.Segment.Start
			}
			out = append(out, MarkdownPart{MarkdownCodeSpan, b.String(), line(start)})
			return ast.WalkSkipChildren, nil
		case *ast.FencedCodeBlock, *ast.CodeBlock, *ast.HTMLBlock:
			kind := MarkdownCodeBlock
			if _, ok := n.(*ast.HTMLBlock); ok {
				kind = MarkdownHTML
			}
			lines := n.Lines()
			var b strings.Builder
			for i := 0; i < lines.Len(); i++ {
				seg := lines.At(i)
				b.Write(seg.Value(src))
			}
			start := 0
			if lines.Len() > 0 {
				start = lines.At(0).Start
			}
			out = append(out, MarkdownPart{kind, b.String(), line(start)})
			return ast.WalkSkipChildren, nil
		case *ast.RawHTML:
			var b strings.Builder
			for i := 0; i < n.Segments.Len(); i++ {
				seg := n.Segments.At(i)
				b.Write(seg.Value(src))
			}
			start := 0
			if n.Segments.Len() > 0 {
				start = n.Segments.At(0).Start
			}
			out = append(out, MarkdownPart{MarkdownHTML, b.String(), line(start)})
		}
		return ast.WalkContinue, nil
	})
	return out
}

// MarkdownTables is every table of a Markdown file, as GitHub's table
// extension reads it: each table its rows, the header first, and each row
// its cells. A cell is its text, with strong emphasis kept as ** so a table
// that marks a name in bold can be read for it.
func (r *Tree) MarkdownTables(t testing.TB, rel string) [][][]string {
	t.Helper()
	return MarkdownTablesOf(r.read(t, rel))
}

// MarkdownTablesOf reads the tables of Markdown a parse handed over.
func MarkdownTablesOf(src []byte) [][][]string {
	doc := goldmark.New(goldmark.WithExtensions(extension.Table)).Parser().Parse(text.NewReader(src))
	var tables [][][]string
	var cellText func(n ast.Node) string
	cellText = func(n ast.Node) string {
		var b strings.Builder
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			switch c := c.(type) {
			case *ast.Text:
				b.Write(c.Segment.Value(src))
			case *ast.Emphasis:
				if c.Level == 2 {
					b.WriteString("**" + cellText(c) + "**")
				} else {
					b.WriteString(cellText(c))
				}
			case *ast.CodeSpan:
				b.WriteString("`" + cellText(c) + "`")
			default:
				b.WriteString(cellText(c))
			}
		}
		return b.String()
	}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		table, ok := n.(*east.Table)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}
		var rows [][]string
		for row := table.FirstChild(); row != nil; row = row.NextSibling() {
			var cells []string
			for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
				cells = append(cells, strings.TrimSpace(cellText(cell)))
			}
			rows = append(rows, cells)
		}
		tables = append(tables, rows)
		return ast.WalkSkipChildren, nil
	})
	return tables
}
