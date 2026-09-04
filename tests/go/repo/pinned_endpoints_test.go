package repo

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A package that can call out must pin where, in its own tests.
//
// The lesson is from 2026-09-04. The pending-deploy check shelled out to `gh`,
// so a test expressed "cannot ask" by emptying PATH. When the shell-out became
// an HTTP call, that arrangement silently stopped establishing the condition it
// named - and the unit test called api.github.com for real.
//
// It failed, and it failed for the right reason, which is why this exists at
// all. But it failed only because the live call happened to return "nothing
// pending". Had a deploy genuinely been queued at that moment, the test would
// have PASSED - permanently green, guarding nothing, its outcome decided by
// the state of the internet rather than by the code.
//
// That is the near-miss worth writing down. A test whose arrangement goes
// stale does not announce itself: the assertion still reads correctly, the
// name still describes the property, and only the setup has quietly stopped
// creating the situation being asserted about.
//
// So: a package-level variable holding an http(s) endpoint is a seam that
// exists to be moved in tests. If the package's tests never move it, they are
// running against the real thing - and the network is an input, which a test
// states rather than inherits.
//
// Scope, stated honestly. This looks at package-level `var` only, because that
// is the seam a test can move. A `const` endpoint cannot be pinned at all, but
// flagging one would be wrong: scripts/clerk declares its endpoints as consts
// and threads them through a struct field its tests set directly, which is the
// better design rather than a violation. Detecting that shape statically is
// not worth the false positives, so this guards the reassignable seam and says
// nothing about the others.
func TestPackagesWithANetworkEndpointPinItInTests(t *testing.T) {
	root := repoRoot(t)

	var checked int
	err := filepath.WalkDir(filepath.Join(root, "scripts"), func(dir string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}

		fset := token.NewFileSet()
		pkgs, perr := parser.ParseDir(fset, dir, nil, 0)
		if perr != nil {
			return nil // not a Go package, or does not parse; other guards own that
		}

		for _, pkg := range pkgs {
			endpoints := map[string]string{} // variable name -> file it is declared in
			var tests []string

			for name, file := range pkg.Files {
				if strings.HasSuffix(name, "_test.go") {
					tests = append(tests, name)
					continue
				}
				for _, decl := range file.Decls {
					gen, ok := decl.(*ast.GenDecl)
					if !ok || gen.Tok != token.VAR {
						continue
					}
					for _, spec := range gen.Specs {
						vs, ok := spec.(*ast.ValueSpec)
						if !ok {
							continue
						}
						for i, id := range vs.Names {
							if i >= len(vs.Values) {
								continue
							}
							lit, ok := vs.Values[i].(*ast.BasicLit)
							if !ok || lit.Kind != token.STRING {
								continue
							}
							v := strings.Trim(lit.Value, "`\"")
							if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
								endpoints[id.Name] = name
							}
						}
					}
				}
			}

			for name, file := range endpoints {
				checked++
				if len(tests) == 0 {
					rel, _ := filepath.Rel(root, file)
					t.Errorf("%s declares the endpoint %q and the package has no tests at all.\n\n"+
						"Anything exercising this reaches the real service.", rel, name)
					continue
				}
				if !pinnedBySomeTest(t, tests, name) {
					rel, _ := filepath.Rel(root, file)
					t.Errorf("%s declares the endpoint %q and no test in the package ever assigns it.\n\n"+
						"So the package's tests run against the real service, and their outcome depends on "+
						"what it happens to return. Assign it in a TestMain - a closed port on the loopback "+
						"refuses immediately - and let a test that wants an answer stand up an httptest server.", rel, name)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking scripts: %v", err)
	}
	if checked == 0 {
		t.Fatal("no network endpoints found in scripts/, so this test proves nothing")
	}
}

// pinnedBySomeTest reports whether any test file assigns the named variable.
func pinnedBySomeTest(t *testing.T, files []string, name string) bool {
	t.Helper()
	for _, f := range files {
		tree, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
		if err != nil {
			continue
		}
		var found bool
		ast.Inspect(tree, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, lhs := range assign.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name == name {
					found = true
					return false
				}
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}
