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

// A guard that examined nothing must not report success.
//
// WHY THIS CLASS HAS ITS OWN CHECK. Three separate defects in this estate came
// from the same reasoning error, in three different substrates:
//
//   - a plan summary reported "No changes. The estate already matches the
//     config." from a document whose output_changes it had never parsed (#320);
//   - `check-hypervisor` matched no hosts, skipped the play, printed an empty
//     recap and exited 0 (#322);
//   - the approved-registries check reported on every image in the cluster
//     while only ever reading the ones written down in this repository (#319).
//
// None of them was a wrong answer. Each was a **claim wider than the thing
// actually inspected**, and in every case the output was indistinguishable
// from the good news it resembled. "Checked and clean" and "did not look" are
// the same green.
//
// WHAT THIS CAN AND CANNOT CATCH, said out loud because a guard whose edges
// nobody knows is worse than none.
//
// It catches the mechanical form: a test that discovers its own subject from
// the filesystem and never asserts how much it found. That form is reachable by
// ordinary maintenance rather than by anything exotic - a directory renamed, an
// extension changed, files moved one level down so every entry is skipped as a
// directory - and it converts a guard into a green check with no assertions in
// it.
//
// It cannot catch the other two. Whether a claim's *scope* matches its reach
// (#319), or whether a function has an axis it never examines (#320), are
// judgements about intent that live in prose. Those stay review's job, and
// saying so is more honest than implying this covers the family.
//
// WHY NOT SIMPLY REQUIRE THE SHARED HELPERS. tracked() and walkText() now
// refuse to return having found nothing, which covers their callers for free.
// But most guards here walk the tree themselves, because they want a different
// filter or a different root, and rewriting two dozen working tests to fit one
// helper would be a large change that breaks the thing it is trying to protect.
// So the rule is the property - assert what you examined - rather than the
// mechanism.

// discoveryCalls are the ways a test finds its own subject on disk. A test
// using one of these is asking the filesystem what to check, so what comes back
// is not something the author wrote down and can be sure of.
var discoveryCalls = map[string]bool{
	"filepath.Walk":    true,
	"filepath.WalkDir": true,
	"filepath.Glob":    true,
	"os.ReadDir":       true,
}

// callName renders a call's function as it is written, so `filepath.WalkDir`
// stays distinguishable from a local `WalkDir`.
func callName(c *ast.CallExpr) string {
	switch f := c.Fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		if x, ok := f.X.(*ast.Ident); ok {
			return x.Name + "." + f.Sel.Name
		}
		return f.Sel.Name
	}
	return ""
}

// discovers reports whether the function finds its subject on disk.
func discovers(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && discoveryCalls[callName(c)] {
			found = true
		}
		return !found
	})
	return found
}

// intConstants collects the names in a file that are declared as constant
// integers, at file scope or inside any function.
//
// It exists so a floor may be written as a named constant. The checker has no
// type information - the files are parsed, not type-checked - so a bare
// identifier is otherwise indistinguishable from a variable holding anything
// at all. Reading the `const` declarations is the cheapest way to tell, and it
// is exact for the form that matters: a literal assigned to a name in the same
// file the guard is written in.
//
// A constant declared in another file of this package is not found, and that
// is deliberate rather than an oversight. Widening the search to the package
// would let a floor be satisfied by a name whose value a reader of the guard
// cannot see, which is the opposite of what the floor is for.
func intConstants(f *ast.File) map[string]bool {
	names := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		decl, ok := n.(*ast.GenDecl)
		if !ok || decl.Tok != token.CONST {
			return true
		}
		for _, spec := range decl.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i < len(vs.Values) && isIntLiteral(vs.Values[i]) {
					names[name.Name] = true
				}
			}
		}
		return true
	})
	return names
}

// assertsAFloor reports whether the function compares some count against an
// integer floor and fails the test when it falls short.
//
// The phrasings already in this package are all accepted, deliberately:
// `checked == 0`, `len(x) == 0`, `checked < 3`, and `checked < atLeastThisMany` where
// `atLeastThisMany` is a constant integer declared in the same file. Requiring one
// spelling would mean rewriting working guards to satisfy a checker, which is
// the tail wagging the dog - what matters is that a floor exists, not how it
// reads.
//
// The named form was refused until #364. That contradicted the paragraph above
// in the direction that matters: it pushed guards toward a magic number and
// away from a name explaining what the number means. `scanned < 10` and
// `scanned < atLeastTenWorkflows` assert exactly the same property, and only
// one of them tells the next reader why ten.
func assertsAFloor(fn *ast.FuncDecl, consts map[string]bool) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		ifStmt, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		cmp, ok := ifStmt.Cond.(*ast.BinaryExpr)
		if !ok {
			return true
		}
		switch cmp.Op {
		case token.EQL, token.LSS, token.LEQ, token.NEQ:
		default:
			return true
		}
		if !isIntFloor(cmp.X, consts) && !isIntFloor(cmp.Y, consts) {
			return true
		}
		if failsTheTest(ifStmt.Body) {
			found = true
			return false
		}
		return true
	})
	return found
}

// isIntFloor reports whether an operand is an integer floor: a literal, or a
// name declared as a constant integer in the same file.
func isIntFloor(e ast.Expr, consts map[string]bool) bool {
	if isIntLiteral(e) {
		return true
	}
	id, ok := e.(*ast.Ident)
	return ok && consts[id.Name]
}

func isIntLiteral(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.INT
}

// failsTheTest reports whether a block calls t.Fatal, t.Fatalf, t.Error or
// t.Errorf. A block that only logs is not an assertion.
func failsTheTest(block *ast.BlockStmt) bool {
	found := false
	ast.Inspect(block, func(n ast.Node) bool {
		if found {
			return false
		}
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch callName(c) {
		case "t.Fatal", "t.Fatalf", "t.Error", "t.Errorf":
			found = true
			return false
		}
		return true
	})
	return found
}

func TestEveryGuardThatDiscoversItsSubjectAssertsItFoundSome(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "tests", "go", "repo")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	var vacuous []string
	discovering := 0

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, parseErr := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", e.Name(), parseErr)
		}
		consts := intConstants(f)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			if !discovers(fn) {
				continue
			}
			discovering++
			if !assertsAFloor(fn, consts) {
				vacuous = append(vacuous, e.Name()+"  "+fn.Name.Name)
			}
		}
	}

	// This check discovers its own subject too, so it holds itself to the same
	// rule. Without this it is exactly the thing it exists to refuse.
	if discovering == 0 {
		t.Fatal("found no tests in this package that discover their subject on disk, which cannot be right - this check is reading the wrong directory and proves nothing")
	}

	sort.Strings(vacuous)
	if len(vacuous) > 0 {
		t.Errorf(`%d guard(s) discover their subject from the filesystem and never assert they found any:

  %s

Each walks the tree, loops over what it finds, and asserts something about every
entry - so finding nothing runs zero assertions and reports success. A renamed
directory, a changed extension, or files moved one level down is enough, and
none of those announces itself.

Add a floor and fail on it. Any of the phrasings already used here will do:

    if checked == 0 {
        t.Fatal("no <subject> found, so this test proves nothing")
    }

A named constant declared in the same file counts too, and reads better than a
bare number because it can say what the number means:

    const atLeastOnePerWorkflow = 13
    if checked < atLeastOnePerWorkflow {
        t.Fatalf("only %%d checked", checked)
    }

Prefer a real minimum over zero where you know one - "only %%d were checked" is
strictly better than "at least one was", because the interesting failure is
usually a filter that still matches something.`, len(vacuous), strings.Join(vacuous, "\n  "))
	}
}

// TestAFloorIsRecognisedHoweverItIsSpelled is the counterexample table for
// assertsAFloor. It is here rather than in the mutation ledger because the
// ledger cannot reach Go source: the guards run from a binary compiled once
// from the real tree, so an edit to a scratch copy never changes what they do.
//
// The rows that matter are the last three. A floor written as a named constant
// was refused before #364 while asserting exactly the same property as the
// literal beside it, and a bare identifier that is NOT a constant integer must
// still be refused - otherwise any comparison against any variable would pass
// for a floor and the guard would stop meaning anything.
func TestAFloorIsRecognisedHoweverItIsSpelled(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{
			name: "equality against zero",
			src:  `if checked == 0 { t.Fatal("nothing checked") }`,
			want: true,
		},
		{
			name: "less than a literal",
			src:  `if checked < 3 { t.Fatalf("only %d", checked) }`,
			want: true,
		},
		{
			name: "len against a literal",
			src:  `if len(found) == 0 { t.Fatal("nothing found") }`,
			want: true,
		},
		{
			name: "no comparison at all",
			src:  `for _, f := range found { _ = f }`,
			want: false,
		},
		{
			name: "compares but only logs",
			src:  `if checked == 0 { t.Log("nothing checked") }`,
			want: false,
		},
		{
			name: "named constant declared in the function",
			src:  `const atLeastThisMany = 10` + "\n" + `if checked < atLeastThisMany { t.Fatalf("only %d", checked) }`,
			want: true,
		},
		{
			name: "named constant declared at file scope",
			src:  `if checked < atLeastAtFileScope { t.Fatalf("only %d", checked) }`,
			want: true,
		},
		{
			name: "a variable is not a floor",
			src:  `expected := len(other)` + "\n" + `if checked < expected { t.Fatalf("only %d", checked) }`,
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package p\n\nconst atLeastAtFileScope = 10\n\nfunc TestX(t *testing.T) {\n" + tc.src + "\n}\n"
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "x_test.go", src, 0)
			if err != nil {
				t.Fatalf("parsing the fixture: %v", err)
			}
			fn := f.Decls[len(f.Decls)-1].(*ast.FuncDecl)
			if got := assertsAFloor(fn, intConstants(f)); got != tc.want {
				t.Errorf("assertsAFloor = %v, want %v, for:\n%s", got, tc.want, tc.src)
			}
		})
	}
}
