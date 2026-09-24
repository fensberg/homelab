package repo

import (
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Three shapes a non-reusable block takes, refused where they are found.
//
// These exist because of one failure that turned out to be a family. The
// integration tier kept its own copy of the config types, defended as an
// independent reader. When the config changed shape the program's reader was
// updated and the copy was not, and the nightly reported healthy backups as
// broken. Looking for why the copy existed at all found the reason was
// structural - the program kept everything under internal/, which Go forbids
// another module to import - and looking for other copies found eight
// hand-counted routes to the repository root, a second slug function, a second
// machine count that had already fallen behind, and a GitHub API client
// rebuilt in seven programs.
//
// None of those was a decision. Each was the path of least resistance at the
// moment it was written, and nothing said so. These guards say so.
//
// Each one enumerates every tracked Go file rather than checking a list of
// known offenders, because a list is exactly what goes stale. Where the answer
// today is "some exist", they are declared as debt that must match exactly:
// adding one fails, and so does fixing one without deleting its entry. That is
// the ratchet - the number can only go down, and every change that retires a
// duplicate says so in the diff.

// --- one reader per document --------------------------------------------------

// The rendered config is decoded by one set of types.
//
// Two structs describing the same JSON is how the nightly came to hand object
// storage an empty bucket name: Go's decoder fills a field the document no
// longer has with its zero value and says nothing. The config's own top-level
// keys are distinctive enough to find any second declaration of it.
func TestTheConfigDocumentHasOneReader(t *testing.T) {
	const home = "scripts/contractor/config/"
	// Keys only the management config uses. A struct field carrying either is
	// a reader of that document, whatever the struct is named. Real struct tags
	// only, read from the syntax tree - a text match would flag this file for
	// naming them.
	distinctive := []string{`json:"sites"`, `json:"object_storage"`}

	checked := 0
	for _, rel := range goFiles(t) {
		checked++
		if strings.HasPrefix(rel, home) {
			continue
		}
		tags := structTags(t, rel)
		for _, tag := range distinctive {
			if tags[tag] {
				t.Errorf("%s declares a field tagged %s, so it is a second reader of the rendered config.\n\n"+
					"Import homelab/contractor/config instead - tests/go does, through a local replace "+
					"in its go.mod. A second declaration of the same document is how the nightly "+
					"came to report healthy backups as broken: the program's reader changed, the copy "+
					"did not, and the decoder filled the missing fields with empty strings.", rel, tag)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no Go files were examined, so this guard proved nothing")
	}
}

// structTags is every struct field tag in a Go file, split into its
// key:"value" parts so a field tagged `json:"sites,omitempty" yaml:"x"` still
// counts as json:"sites".
func structTags(t *testing.T, rel string) map[string]bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), rel, readRepoFile(t, rel), parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", rel, err)
	}
	out := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		field, ok := n.(*ast.Field)
		if !ok || field.Tag == nil {
			return true
		}
		raw, err := strconv.Unquote(field.Tag.Value)
		if err != nil {
			return true
		}
		for _, part := range strings.Fields(raw) {
			key, val, ok := strings.Cut(part, ":")
			if !ok {
				continue
			}
			name, _, _ := strings.Cut(strings.Trim(val, `"`), ",")
			out[key+`:"`+name+`"`] = true
		}
		return true
	})
	return out
}

// --- the repository root is found, not counted -----------------------------------

// Nothing finds the repository root by counting directories.
//
// A fixed-depth climb is correct until the file holding it moves, and then it
// is wrong without a word. Nine copies of that answer existed across three
// modules, in two spellings - a chain of ".." and nested filepath.Dir - and the
// guard's first version knew only the first, which let the one copy in the
// shipped binary through. repopath.Root walks up to the marker instead.
//
// No debt: every copy has been replaced.
func TestNothingFindsTheRepositoryByCountingDirectories(t *testing.T) {
	found := map[string]bool{}
	for _, rel := range goFiles(t) {
		if climbsFixedDepth(t, rel) {
			found[rel] = true
		}
	}
	ratchet(t, "a fixed-depth climb to the repository root", found, map[string]bool{},
		"Use repopath.Root or repopath.Join from homelab/details/repopath.")
}

// climbsFixedDepth reports whether a file climbs a fixed number of
// directories, in either spelling: two ".." literals in a row, as
// filepath.Join("..", "..", ...) takes, or filepath.Dir(filepath.Dir(...)).
// Tokenized rather than matched, so explaining the pattern in a comment is not
// a violation.
func climbsFixedDepth(t *testing.T, rel string) bool {
	t.Helper()
	src := []byte(readRepoFile(t, rel))
	var s scanner.Scanner
	fset := token.NewFileSet()
	s.Init(fset.AddFile(rel, -1, len(src)), src, nil, 0)

	// The last seven tokens: enough to see Dir ( filepath . Dir.
	var window []string
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			return false
		}
		cur := tok.String()
		if tok == token.STRING || tok == token.IDENT {
			cur = lit
		}
		window = append(window, cur)
		if len(window) > 7 {
			window = window[1:]
		}
		n := len(window)
		if n >= 3 && window[n-1] == `".."` && window[n-2] == "," && window[n-3] == `".."` {
			return true
		}
		if n >= 7 && strings.Join(window[n-7:], " ") == "filepath . Dir ( filepath . Dir" {
			return true
		}
	}
}

// --- a fact crossing a module boundary is declared once ----------------------------

// A string restated in more than one Go module is a building block nobody
// built.
//
// The first measurement found 101: a GitHub API client in seven programs, git
// test isolation in four, repository paths, vendor URLs, and the
// organization's own name. Most were not wrong yet. All of them were a second
// place for the next change to miss, which is the whole of what went wrong in
// the integration tier.
//
// Declared in tests/building-block-debt.yml. The declaration must match what
// the tree holds exactly: a new crossing fails, and so does retiring one
// without deleting its entry, so the list only ever shrinks and every change
// that shrinks it shows in its diff.
func TestAFactCrossingAModuleBoundaryIsDeclared(t *testing.T) {
	crossing := crossModuleLiterals(t)

	var declared struct {
		Debt []string `yaml:"debt"`
	}
	raw := readRepoFile(t, "tests/building-block-debt.yml")
	if err := yaml.Unmarshal([]byte(raw), &declared); err != nil {
		t.Fatalf("tests/building-block-debt.yml does not parse: %v", err)
	}
	debt := map[string]bool{}
	for _, d := range declared.Debt {
		if debt[d] {
			t.Errorf("tests/building-block-debt.yml lists %q twice", d)
		}
		debt[d] = true
	}

	found := map[string]bool{}
	for lit := range crossing {
		found[lit] = true
	}
	ratchet(t, "a string restated in more than one Go module", found, debt,
		"Make it one exported name in the module that owns the fact, and import it - "+
			"tests/go can import homelab/contractor's public packages. If it genuinely "+
			"belongs in each, it is still debt: add it to tests/building-block-debt.yml "+
			"and say why in the commit.")
	if t.Failed() {
		var lines []string
		for lit, mods := range crossing {
			if !debt[lit] {
				lines = append(lines, "  "+strconv.Quote(lit)+"  in "+strings.Join(mods, ", "))
			}
		}
		sort.Strings(lines)
		if len(lines) > 0 {
			t.Logf("where each undeclared one appears:\n%s", strings.Join(lines, "\n"))
		}
	}
}

// crossModuleLiterals maps each qualifying string literal to the modules that
// contain it, keeping only those found in two or more.
//
// Qualifying means: at least twelve characters, no format verb, and no
// newline. Shorter strings are words rather than facts; format strings and
// messages are prose; import paths are the language, not the estate. What is
// left is paths, URLs, header names, environment variables, vendor
// identifiers - the things that must agree.
func crossModuleLiterals(t *testing.T) map[string][]string {
	t.Helper()
	modules := goModuleDirs(t)

	seen := map[string]map[string]bool{}
	for _, rel := range goFiles(t) {
		mod := owningModule(rel, modules)
		if mod == "" {
			// Not skipped. A Go file no module owns is one this guard cannot
			// place, and skipping it would be a hole every future file of that
			// kind walks through - the guard reporting green over code it never
			// examined.
			t.Errorf("%s belongs to no Go module, so this guard cannot tell which module "+
				"its strings cross into. Put it inside one.", rel)
			continue
		}
		for _, lit := range stringLiterals(t, rel) {
			if len(lit) < 12 || strings.ContainsAny(lit, "%\n") {
				continue
			}
			if seen[lit] == nil {
				seen[lit] = map[string]bool{}
			}
			seen[lit][mod] = true
		}
	}

	out := map[string][]string{}
	for lit, mods := range seen {
		if len(mods) < 2 {
			continue
		}
		var list []string
		for m := range mods {
			list = append(list, m)
		}
		sort.Strings(list)
		out[lit] = list
	}
	if len(seen) == 0 {
		t.Fatal("no string literals were found in any Go file, so this guard proved nothing")
	}
	return out
}

// stringLiterals returns every string literal in a Go file outside its import
// declarations, unquoted.
func stringLiterals(t *testing.T, rel string) []string {
	t.Helper()
	src := []byte(readRepoFile(t, rel))
	var s scanner.Scanner
	fset := token.NewFileSet()
	s.Init(fset.AddFile(rel, -1, len(src)), src, nil, 0)

	var out []string
	inImport, depth := false, 0
	for {
		_, tok, lit := s.Scan()
		switch tok {
		case token.EOF:
			return out
		case token.IMPORT:
			inImport = true
			continue
		case token.LPAREN:
			if inImport {
				depth++
			}
		case token.RPAREN:
			if inImport {
				depth--
				if depth == 0 {
					inImport = false
				}
			}
		case token.SEMICOLON:
			// A single-line import ends at its semicolon.
			if inImport && depth == 0 {
				inImport = false
			}
		case token.STRING:
			if inImport {
				continue
			}
			if v, err := strconv.Unquote(lit); err == nil {
				out = append(out, v)
			}
		}
	}
}

// --- shared plumbing for the three -----------------------------------------------

// goFiles is every tracked Go file, relative to the repository root.
func goFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, rel := range trackedFiles(t) {
		if strings.HasSuffix(rel, ".go") {
			out = append(out, rel)
		}
	}
	return out
}

// goModuleDirs is the directory of every tracked go.mod, relative to the root.
//
// The one enumeration of Go modules in this package. goModules, which the
// taskfile guards use, is derived from it rather than walking the tree a second
// way - two enumerators of one set is the thing this file exists to refuse.
func goModuleDirs(t *testing.T) []string {
	t.Helper()
	var mods []string
	for _, rel := range trackedFiles(t) {
		if filepath.Base(rel) == "go.mod" {
			mods = append(mods, filepath.Dir(rel))
		}
	}
	sort.Strings(mods)
	if len(mods) < 2 {
		t.Fatalf("found %d Go module(s); this repository has several, so the enumeration broke", len(mods))
	}
	return mods
}

// owningModule is the nearest module above a file - the longest matching
// directory, since a file under tests/go belongs to tests/go and not to any
// module that might one day sit above it.
func owningModule(rel string, modules []string) string {
	best := ""
	for _, m := range modules {
		if strings.HasPrefix(rel, m+string(os.PathSeparator)) && len(m) > len(best) {
			best = m
		}
	}
	return best
}

// ratchet compares what the tree holds against what is declared, in both
// directions. Exact rather than "at most", for the reason coverage-exemptions.yml
// gives: "at most" drifts, while exact means an improvement has to be written
// down, so the declaration tightens as the code does.
func ratchet(t *testing.T, what string, found, declared map[string]bool, fix string) {
	t.Helper()
	var added, retired []string
	for k := range found {
		if !declared[k] {
			added = append(added, strconv.Quote(k))
		}
	}
	for k := range declared {
		if !found[k] {
			retired = append(retired, strconv.Quote(k))
		}
	}
	sort.Strings(added)
	sort.Strings(retired)

	if len(added) > 0 {
		t.Errorf("%d new instance(s) of %s:\n\n  %s\n\n%s",
			len(added), what, strings.Join(added, "\n  "), fix)
	}
	if len(retired) > 0 {
		t.Errorf("%d declared instance(s) of %s no longer exist - delete them from the declaration, "+
			"which is the ratchet moving:\n\n  %s", len(retired), what, strings.Join(retired, "\n  "))
	}
}
