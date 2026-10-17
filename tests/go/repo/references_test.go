package repo

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Nothing names a guard, or a guard's file, that is not there.
//
// A comment that says a guard's file refuses something is a claim about
// the tree, and a guard moved, renamed or retired leaves the claim standing
// over nothing. Moving the configuration rules to Conftest left stale ones in
// workflows, CLAUDE.md, tests/README.md and the code, found by four sweeps by
// hand - each finding what the one before had missed. So the claim is checked:
// every test named in a comment, a document or a configuration file is a test
// some module declares, and every guard file named is one that exists.
//
// HOW IT READS. Go is read through its own parser and only its comments count:
// a test's fixtures are strings, and a name made up in one is data, not a
// claim. Everything else is read whole, because a YAML comment, a Rego comment
// and a sentence in a README are all prose. A qualified name - run.TestPort -
// is a function, not a test, and is not read. A name wrapped across two
// comment lines is joined before it is judged.
//
// WHAT IT DOES NOT READ. The epoch records and the ledger's history are what
// happened, and name what was there at the time; the files .gitattributes
// marks vendored are somebody else's.

// historyPrefixes are where a name is a record of the past, not a claim.
var historyPrefixes = []string{"docs/epochs/", ledgerEntries + "/", ledgerEntries + ".yml", "tests/mutations-removed.yml"}

var (
	testNamed     = regexp.MustCompile(`(^|[^.\w])(Test[A-Z][A-Za-z0-9_]*)`)
	guardFileName = regexp.MustCompile(`\btests/go/repo/[a-z0-9_]+_test\.go\b`)
	leadingWord   = regexp.MustCompile(`^[\s#/*-]*([A-Za-z0-9_]+)`)
	declaredTest  = regexp.MustCompile(`^Test[A-Z_]`)
)

func TestNoReferenceNamesAGuardThatIsGone(t *testing.T) {
	files := trackedFiles(t)
	vendored := vendoredFiles(t, files)
	exists := map[string]bool{}
	for _, rel := range files {
		exists[rel] = true
	}
	declared := declaredTests(t, files)
	if len(declared) < 300 {
		t.Fatalf("only %d tests were found declared across the modules, which is not this repository - the reader has stopped matching", len(declared))
	}

	var problems []string
	read := 0
	for _, rel := range files {
		if vendored[rel] || isHistory(rel) || strings.HasPrefix(rel, semgrepExamples+"/") {
			continue
		}
		text, ok := proseOf(t, rel)
		if !ok {
			continue
		}
		read++
		for _, p := range danglingIn(text, declared, exists) {
			problems = append(problems, rel+": "+p)
		}
	}
	if read < 300 {
		t.Fatalf("only %d files were read, which is not this repository - the enumeration has stopped matching", read)
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p + ". A name of something that is gone is a claim nothing stands behind: point it at what does the job now, or take the claim out.")
	}
}

// danglingIn is each test or guard file a piece of prose names that is not
// declared or not there.
func danglingIn(text string, declared, exists map[string]bool) []string {
	var out []string
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		for _, m := range testNamed.FindAllStringSubmatchIndex(line, -1) {
			name := line[m[4]:m[5]]
			if declared[name] {
				continue
			}
			if m[5] == len(strings.TrimRight(line, " \t")) && i+1 < len(lines) {
				if w := leadingWord.FindStringSubmatch(lines[i+1]); w != nil && declared[name+w[1]] {
					continue
				}
			}
			out = append(out, fmt.Sprintf("names %s, which no module declares", name))
		}
		for _, f := range guardFileName.FindAllString(line, -1) {
			if !exists[f] {
				out = append(out, fmt.Sprintf("names %s, which is not there", f))
			}
		}
	}
	return out
}

// declaredTests is every test function any Go file declares, read by Go's
// parser, so a test in any module may be named.
func declaredTests(t *testing.T, files []string) map[string]bool {
	t.Helper()
	root := repoRoot(t)
	out := map[string]bool{}
	for _, rel := range files {
		if !strings.HasSuffix(rel, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, rel), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s does not parse, so the tests it declares cannot be known: %v", rel, err)
		}
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && declaredTest.MatchString(fn.Name.Name) {
				out[fn.Name.Name] = true
			}
		}
	}
	return out
}

// proseOf is what a file says in words: a Go file's comments, or any other
// text file whole. Binary files say nothing.
func proseOf(t *testing.T, rel string) (string, bool) {
	t.Helper()
	path := filepath.Join(repoRoot(t), rel)
	if strings.HasSuffix(rel, ".go") {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s does not parse, so its comments cannot be read: %v", rel, err)
		}
		var b strings.Builder
		for _, g := range f.Comments {
			b.WriteString(g.Text())
			b.WriteString("\n")
		}
		return b.String(), true
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	if strings.ContainsRune(string(body), 0) {
		return "", false
	}
	return string(body), true
}

func isHistory(rel string) bool {
	for _, p := range historyPrefixes {
		if rel == p || strings.HasPrefix(rel, p) {
			return true
		}
	}
	return false
}

func TestDanglingInFindsWhatIsGoneAndLeavesWhatIsThere(t *testing.T) {
	declared := map[string]bool{"TestThere": true, "TestAVeryLongNameThatWraps": true}
	exists := map[string]bool{"tests/go/repo/there_test.go": true}
	for name, c := range map[string]struct {
		text string
		want int
	}{
		"a test that is there":            {"# proved by TestThere", 0},
		"a test that is gone":             {"# proved by TestGone", 1},
		"a function called on a package":  {"// asked through run.TestPort", 0},
		"a name wrapped across two lines": {"# and TestAVeryLongName\n# ThatWraps covers it", 0},
		"a guard file that is there":      {"see tests/go/repo/there_test.go", 0},
		"a guard file that is gone":       {"see tests/go/repo/gone_test.go", 1},
		"a word that is not a test":       {"Testing that it works", 0},
		"both gone, on one line":          {"TestGone in tests/go/repo/gone_test.go", 2},
	} {
		if got := danglingIn(c.text, declared, exists); len(got) != c.want {
			t.Errorf("%s: want %d, got %v", name, c.want, got)
		}
	}
}
