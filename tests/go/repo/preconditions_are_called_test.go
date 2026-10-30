package repo

import (
	"go/ast"
	"go/token"
	"regexp"
	"strings"
	"testing"
)

// A precondition that nothing calls is a precondition that does not exist.
//
// This is the shape that has escaped a full test run four times this week. The
// list of checks gets tested, the checks themselves get tested, and nothing
// asserts that anything runs them - so deleting the one line that does leaves
// every test green and the guard switched off.
//
// The change detector catches it for unexported functions inside one package.
// These are exported and called across packages, which that check deliberately
// does not follow, because chasing exported symbols everywhere turns a sharp
// check into a noisy one. This is the narrow version: whatever declares itself
// a precondition runner must be called from somewhere that is not its own file
// and is not a test.
func TestEveryPreconditionRunnerIsCalled(t *testing.T) {
	light(t)
	tree := repository(t)
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, rel := range tree.Files(t) {
		if strings.HasPrefix(rel, "scripts/contractor/") && strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go") {
			files[rel] = tree.Go(t, fset, rel)
		}
	}

	declared := map[string]string{} // name -> file that declares it
	callers := map[string]bool{}
	for rel, f := range files {
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && preconditionRunner.MatchString(fn.Name.Name) {
				declared[fn.Name.Name] = rel
			}
		}
	}

	if len(declared) == 0 {
		t.Fatal("no precondition runners found at all. Either they have been renamed, " +
			"in which case this check now guards nothing, or every guard has been " +
			"deleted - and passing on an empty set is how a check stops mattering.")
	}

	for rel, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			var name string
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				name = fun.Name
			case *ast.SelectorExpr:
				name = fun.Sel.Name
			}
			if declaredIn, ok := declared[name]; ok && declaredIn != rel {
				callers[name] = true
			}
			return true
		})
	}

	for name, declaredIn := range declared {
		if !callers[name] {
			t.Errorf("%s declares %s and nothing outside that file calls it.\n\n"+
				"Its list is tested and its checks are tested, so deleting the one line "+
				"that runs it leaves every test green and the guard switched off. That "+
				"has happened four times this week.", declaredIn, name)
		}
	}
}

// CheckSomethingPreconditions - the runner, as opposed to the list it
// iterates.
var preconditionRunner = regexp.MustCompile(`^Check\w*Preconditions$`)
