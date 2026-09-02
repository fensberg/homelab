package repo

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// A test that asserts your change is present is not coverage.
//
// This estate has produced the same defect three times, and each time the code
// was tested and the tests passed:
//
//   - A push guard whose test asserted the pre-fix rule and passed through an
//     unrelated fail-closed path (#121).
//   - A cordon check with thorough tests for its parser, none of which noticed
//     when the call to that parser was deleted from the phase.
//   - A teardown refresh with a correct test for the arguments it built, which
//     said nothing about whether anything called it - and deleting the call was
//     the regression the test existed to prevent (#146).
//
// The last two share one mechanical shape: **the helper is tested, the wiring
// is not.** A function reachable only from tests is a function whose caller can
// be deleted with every test still green, which is precisely how a guard gets
// silently switched off.
//
// That shape is checkable, and this checks it. It is not a general "is this
// test any good" detector - no such thing exists - but it removes the need to
// think hard about the one variety that has actually hurt here.
func TestNoFunctionIsReachableOnlyFromItsTests(t *testing.T) {
	for _, pkg := range goPackages(t) {
		orphans := testOnlyFunctions(t, pkg)
		for _, name := range orphans {
			t.Errorf("%s: %s is called from its tests and from nowhere else.\n\n"+
				"Either it is dead code with a test attached, or the code that used to "+
				"call it has been removed and every test still passed - which is how a "+
				"guard gets switched off without anybody noticing. Delete it, or assert "+
				"the behaviour at the level that calls it.",
				rel(t, pkg), name)
		}
	}
}

// goPackages lists the directories holding this repository's Go programs.
// tests/go is deliberately absent: it is tests all the way down, so every
// function in it is reachable only from tests by construction.
func goPackages(t *testing.T) []string {
	t.Helper()
	root := filepath.Join(repoRoot(t), "scripts")
	var dirs []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		if info.Name() == "testdata" || strings.HasPrefix(info.Name(), ".") {
			return filepath.SkipDir
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), "_test.go") {
				dirs = append(dirs, path)
				return nil
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(dirs) == 0 {
		t.Fatal("found no Go packages with tests, so this check would pass on nothing")
	}
	return dirs
}

// testOnlyFunctions names every unexported function in a package that appears
// in its test files and in none of its production ones.
//
// Unexported only, deliberately. An exported function may legitimately have no
// caller inside its own package - the caller is elsewhere - and chasing that
// across packages would turn a sharp check into a noisy one. Every case this
// has caught was unexported.
func testOnlyFunctions(t *testing.T, dir string) []string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", dir, err)
	}

	declared := map[string]bool{}
	usedInProd := map[string]bool{}
	usedInTest := map[string]bool{}

	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			isTest := strings.HasSuffix(name, "_test.go")
			for _, d := range file.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if ok && !isTest && fn.Recv == nil && !ast.IsExported(fn.Name.Name) {
					declared[fn.Name.Name] = true
				}
			}
			ast.Inspect(file, func(n ast.Node) bool {
				// A declaration is not a use of itself.
				if fn, ok := n.(*ast.FuncDecl); ok {
					if fn.Body != nil {
						ast.Inspect(fn.Body, func(b ast.Node) bool {
							if id, ok := b.(*ast.Ident); ok {
								mark(usedInProd, usedInTest, id.Name, isTest)
							}
							return true
						})
					}
					// Types in the signature still count as uses.
					ast.Inspect(fn.Type, func(b ast.Node) bool {
						if id, ok := b.(*ast.Ident); ok {
							mark(usedInProd, usedInTest, id.Name, isTest)
						}
						return true
					})
					return false
				}
				// Everything outside a function body: var blocks, consts,
				// composite literals holding function values.
				if id, ok := n.(*ast.Ident); ok {
					mark(usedInProd, usedInTest, id.Name, isTest)
				}
				return true
			})
		}
	}

	var out []string
	for name := range declared {
		if usedInTest[name] && !usedInProd[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func mark(prod, test map[string]bool, name string, isTest bool) {
	if isTest {
		test[name] = true
		return
	}
	prod[name] = true
}

func rel(t *testing.T, path string) string {
	t.Helper()
	r, err := filepath.Rel(repoRoot(t), path)
	if err != nil {
		return path
	}
	return r
}
