package parsed

import (
	"bytes"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

// Shell is a shell script of the tree as mvdan.cc/sh parses it, bash
// dialect, comments kept.
func (r *Tree) Shell(t testing.TB, rel string) *syntax.File {
	t.Helper()
	f, err := ShellOf(rel, string(r.read(t, rel)))
	if err != nil {
		t.Fatalf("%s is not shell that can be read: %v", rel, err)
	}
	return f
}

// ShellOf parses shell a parse handed over as a value: a workflow step's
// run, a task's command, a task's cmd in a playbook. Its name says where it
// came from, in an error.
func ShellOf(name, src string) (*syntax.File, error) {
	return syntax.NewParser(syntax.KeepComments(true), syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(src), name)
}

// Command is one simple command a script runs: its words, each its value
// when it is written with no expansion in it and as the script spells it
// otherwise, so "$root" and $root stay apart; the variables it assigns, in
// front of the command, alone, or declared (readonly, export, local); and
// the line it starts on.
type Command struct {
	Words   []string
	Assigns map[string]string
	Line    int
}

// Commands is every simple command in a parsed script, wherever it sits -
// a function, a pipeline, a condition, a subshell, a substitution - in the
// order it is written.
func Commands(f *syntax.File) []Command {
	var out []Command
	syntax.Walk(f, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.CallExpr:
			c := Command{Assigns: map[string]string{}, Line: int(n.Pos().Line())}
			for _, a := range n.Assigns {
				if a.Name != nil {
					c.Assigns[a.Name.Value] = wordValue(a.Value)
				}
			}
			for _, w := range n.Args {
				c.Words = append(c.Words, wordValue(w))
			}
			out = append(out, c)
		case *syntax.DeclClause:
			// readonly, export, local, declare: a command whose arguments
			// are assignments, or names.
			c := Command{Words: []string{n.Variant.Value}, Assigns: map[string]string{}, Line: int(n.Pos().Line())}
			for _, a := range n.Args {
				switch {
				case a.Naked && a.Name == nil:
					c.Words = append(c.Words, wordValue(a.Value))
				case a.Naked:
					c.Words = append(c.Words, a.Name.Value)
				default:
					c.Assigns[a.Name.Value] = wordValue(a.Value)
				}
			}
			out = append(out, c)
		}
		return true
	})
	return out
}

// wordValue is a word's value when nothing in it expands, and the word as
// written when something does.
func wordValue(w *syntax.Word) string {
	if w == nil {
		return ""
	}
	if lit := w.Lit(); lit != "" || len(w.Parts) == 0 {
		return lit
	}
	var b strings.Builder
	static := true
	for _, p := range w.Parts {
		switch p := p.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, q := range p.Parts {
				if l, ok := q.(*syntax.Lit); ok {
					b.WriteString(l.Value)
				} else {
					static = false
				}
			}
		default:
			static = false
		}
	}
	if static {
		return b.String()
	}
	var printed bytes.Buffer
	if err := syntax.NewPrinter().Print(&printed, w); err != nil {
		return b.String()
	}
	return printed.String()
}
