package main

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
)

// Separating what was written from what was claimed about it.
//
// The clerk asks two questions and the second is only worth asking if the
// first was answered blind. Show a reader a comment saying "this is the fix
// for the overlay outage" and it will read the code looking for that fix; show
// it the same code with nothing attached and it has to work out what is
// actually there. So the commentary is taken away before the first pass and
// handed back for the second.
//
// It matters as much for the structural half. A comment explaining why a block
// exists is exactly what stops a reader noticing that nothing reaches the
// block, that a function calls itself for no reason, or that a value is
// written and read by nobody else.
//
// The commentary is BLANKED, never deleted. Deleting it moves every line after
// it, and then a finding citing line 40 of what the model read points at a
// different line 40 in the file the operator opens. Blanking keeps every byte
// position and every line number true, which is what makes a citation
// checkable rather than approximately right.

// split blanks the commentary out of a file and returns it separately.
//
// ok is false for files that are entirely prose - a Markdown record is a claim
// about the estate rather than a thing that runs - and those go wholly to the
// comparison pass.
func split(path, body string) (code, prose string, ok bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return splitGo(path, body)
	case ".md", ".markdown", ".txt":
		return "", body, false
	case ".yml", ".yaml":
		return blankLines(body, yamlBlockScalars(body), "#")
	case ".sh", ".bash":
		return blankLines(body, shellHeredocs(body), "#")
	case ".toml", ".env", ".cfg", ".conf":
		return blankLines(body, nil, "#")
	case ".ts", ".js":
		return blankLines(body, nil, "//")
	// HCL accepts `#`, `//` and `/* */`, and this repository writes `#`
	// essentially always: 879 whole-line `#` comments across management/cluster
	// and not one `//` at the time this was fixed.
	//
	// It was listed with the `//` languages, so blankLines had nothing to
	// match and every one of those 879 lines went to the CODE side. The blind
	// pass - whose entire purpose is to read the code without being told what
	// it is for - has never once been blind to an OpenTofu file, and the prose
	// side for those files was empty, so the comparison pass had nothing of
	// theirs to check either. Both halves of the clerk were silently off for
	// the language most of this estate's infrastructure is written in.
	case ".tf", ".hcl":
		return blankLines(body, nil, "#", "//")
	default:
		// Unknown, so nothing is asserted about it. Sending it whole to the
		// code side is the conservative error: the comparison pass may be
		// weaker, and the blind pass is never given prose it should not see.
		return body, "", true
	}
}

// splitGo blanks comments by position, found by parsing rather than by pattern.
//
// A regular expression over `//` finds the comment and also finds the one
// inside a string literal and the one inside a URL. The parser cannot make
// that mistake, and this is the language most of the repository is written in.
func splitGo(path, body string) (string, string, bool) {
	fset := token.NewFileSet()
	tree, err := parser.ParseFile(fset, path, body, parser.ParseComments)
	if err != nil {
		// Unparsable Go is still worth reading; fall back rather than
		// refusing, and accept the weaker separation.
		return blankLines(body, nil, "//")
	}

	file := fset.File(tree.Pos())
	out := []byte(body)
	var prose strings.Builder

	for _, group := range tree.Comments {
		prose.WriteString(group.Text())
		prose.WriteString("\n")
		for _, c := range group.List {
			from, to := file.Offset(c.Pos()), file.Offset(c.End())
			for i := from; i < to && i < len(out); i++ {
				// Newlines inside a block comment stay, so the line count of
				// the file the model reads matches the file on disk.
				if out[i] != '\n' {
					out[i] = ' '
				}
			}
		}
	}
	return string(out), prose.String(), true
}

// blankLines blanks whole-line comments only, outside any protected region.
//
// Deliberately not trailing comments: a `#` or `//` later in a line is as
// likely to be inside a string, a URL or a colour as it is to start a comment,
// and mangling the code to catch a few more words of prose is the wrong trade
// when the code is what the blind pass has to reason about.
//
// Several markers because a language may have more than one, and picking the
// wrong single one fails silently: nothing matches, every comment is treated
// as code, and the separation this whole file exists to perform simply does
// not happen. There is no error and no empty result to notice - the blind pass
// is handed a commented file and answers as if it had been given a bare one.
//
// PROTECTED is the half added by #255. A line-prefix stripper has no notion of
// anywhere a `#` is DATA, and this repository has two such places. The clerk
// reported `tests/mutations.yml:49` as commentary no account supported - and it
// was right that nothing supported it, because the line is one context line of
// a deliberately unappliable diff, carried inside a YAML block scalar. Its
// entire purpose is to describe something that does not exist.
//
// That failure is deterministic and recurs on every run over that file, which
// is every pull request that adds a guard. A model being wrong occasionally is
// the price of the role; a mechanical fault firing every time is not.
func blankLines(body string, protected map[int]bool, markers ...string) (string, string, bool) {
	starts := func(line string) bool {
		for _, m := range markers {
			if strings.HasPrefix(line, m) {
				return true
			}
		}
		return false
	}
	lines := strings.Split(body, "\n")
	var prose strings.Builder
	for i, line := range lines {
		if protected[i] {
			continue
		}
		if starts(strings.TrimSpace(line)) {
			prose.WriteString(strings.TrimSpace(line))
			prose.WriteString("\n")
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n"), prose.String(), true
}

// A block scalar header: `|`, `>`, either with an optional chomping indicator
// and an optional explicit indentation indicator in either order, introduced by
// a mapping key or a sequence entry and ending the line but for a comment.
var blockScalarHeader = regexp.MustCompile(`(?::|^\s*-)[ \t]*[|>]([1-9]?)([+-]?)([1-9]?)[ \t]*(?:#.*)?$`)

// yamlBlockScalars returns the line indices holding the CONTENT of a YAML
// block scalar, where a leading `#` is data rather than a comment.
//
// Scanned rather than parsed, and the distinction is worth being exact about
// because #255 is a story about guessing. What is guessed here is nothing: a
// block scalar's extent is a lexical property of YAML - content is every line
// indented further than the key that introduced it, up to the first line that
// is not - and that is decidable without interpreting a single value. What was
// guessed BEFORE was whether a `#` began a comment, which is not decidable
// that way at all, and that is the guess this removes.
//
// A parser would be the better answer if one were free. It is not: all nine Go
// programs under scripts/ carry no third-party dependency, deliberately, and
// adding one to the party that reads pull requests is a supply-chain decision
// that belongs in its own change rather than inside a bug fix.
func yamlBlockScalars(body string) map[int]bool {
	lines := strings.Split(body, "\n")
	protected := map[int]bool{}

	for i := 0; i < len(lines); i++ {
		m := blockScalarHeader.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		headerIndent := indentOf(lines[i])

		// An explicit indentation indicator is relative to the parent node.
		// Either capture group may hold it, since YAML permits `|2-` and `|-2`.
		content := -1
		for _, digits := range []string{m[1], m[3]} {
			if digits != "" {
				content = headerIndent + int(digits[0]-'0')
			}
		}

		j := i + 1
		for ; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "" {
				continue // A blank line never ends a block scalar.
			}
			indent := indentOf(lines[j])
			if content < 0 {
				// No explicit indicator, so the first non-empty line sets it.
				if indent <= headerIndent {
					break
				}
				content = indent
			}
			if indent < content {
				break
			}
		}
		for k := i + 1; k < j; k++ {
			protected[k] = true
		}
		i = j - 1
	}
	return protected
}

// A heredoc introducer: `<<` or `<<-`, then the delimiter, quoted or not.
// The quotes are not required to match, because Go's regexp engine has no
// backreferences - and a mismatched pair is not shell anybody writes, so
// accepting one costs nothing a real script would notice.
var heredocHeader = regexp.MustCompile(`<<([-~]?)[ \t]*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)

// shellHeredocs returns the line indices holding the body of a shell heredoc.
//
// The same shape as a YAML block scalar and named in #255 alongside it: this
// repository writes heredocs full of YAML and of shell, so a `#` inside one is
// routinely data. `.sh` went through the identical line-prefix stripper.
func shellHeredocs(body string) map[int]bool {
	lines := strings.Split(body, "\n")
	protected := map[int]bool{}

	for i := 0; i < len(lines); i++ {
		m := heredocHeader.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		dashed, delim := m[1] != "", m[2]

		j := i + 1
		for ; j < len(lines); j++ {
			end := lines[j]
			if dashed {
				end = strings.TrimLeft(end, " \t")
			}
			if end == delim {
				break
			}
		}
		for k := i + 1; k < j && k < len(lines); k++ {
			protected[k] = true
		}
		i = j
	}
	return protected
}

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}
