package repo

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Every command the e2e tier gives the contractor names a verb it has.
//
// The tier is destructive and run by hand, so nothing runs it on a schedule,
// and nothing noticed when the contractor started taking a verb first: every
// call in the tier put -site first, and the teardown used a -destroy flag that
// had become the demolish-site verb. It could not run one command, while the
// coverage exemptions credited it with most of the contractor (#563).
//
// So the check is made here, in the tier that runs on every push: each
// runContractor call's verb is a string literal, and it is one of the
// contractor's knownVerbs, read from its own source.
func TestTheE2ETierNamesVerbsTheContractorHas(t *testing.T) {
	root := repoRoot(t)
	verbs := contractorVerbs(t, filepath.Join(root, "scripts", "contractor", "main.go"))

	files, err := filepath.Glob(filepath.Join(root, "tests", "go", "e2e", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, f := range files {
		for _, problem := range e2eVerbProblems(t, f, verbs, &calls) {
			t.Error(problem)
		}
	}
	if calls == 0 {
		t.Fatal("no runContractor call was found in tests/go/e2e, so this checked nothing: the helper was renamed, or the tier stopped calling it")
	}
}

// contractorVerbs reads the knownVerbs slice out of the contractor's main.go.
func contractorVerbs(t *testing.T, path string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var verbs []string
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "knownVerbs" || len(vs.Values) != 1 {
			return true
		}
		lit, _ := vs.Values[0].(*ast.CompositeLit)
		for _, e := range lit.Elts {
			if b, ok := e.(*ast.BasicLit); ok {
				v, _ := strconv.Unquote(b.Value)
				verbs = append(verbs, v)
			}
		}
		return false
	})
	if len(verbs) == 0 {
		t.Fatalf("no knownVerbs in %s: the contractor's verb list moved, so nothing here can check the e2e tier against it", path)
	}
	return verbs
}

// e2eVerbProblems names every runContractor call in a file whose verb is not
// a literal the contractor knows.
func e2eVerbProblems(t *testing.T, path string, verbs []string, calls *int) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var problems []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "runContractor" {
			return true
		}
		*calls++
		where := fset.Position(call.Pos()).String()
		if len(call.Args) < 2 {
			problems = append(problems, where+": runContractor is called without a verb")
			return true
		}
		lit, ok := call.Args[1].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			problems = append(problems, where+": the verb is not a string literal, so nothing can check it")
			return true
		}
		verb, _ := strconv.Unquote(lit.Value)
		if !slices.Contains(verbs, verb) {
			problems = append(problems, where+": "+strconv.Quote(verb)+" is not a contractor verb; it has "+strings.Join(verbs, ", "))
		}
		return true
	})
	return problems
}
