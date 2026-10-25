package repo

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// heavy skips a guard under `go test -short`, which is what the pre-push hook
// runs, and says why the guard can wait for the pull request.
//
// THE BUDGET. Committing and pushing together should take under ten seconds,
// and a hook nobody waits on is a hook nobody bypasses. The full suite took
// 160 seconds at push, and two checks on the checks were 107 of them. So a
// push runs only what cannot be undone once it is public - a real name, a
// secret, an unsigned commit - and the guards that answer in milliseconds.
// CI's Test lane runs everything, without -short, on every pull request.
//
// WHAT MAY BE HEAVY. A guard whose failure a later push can fix, and which
// costs whole seconds. Never one that stops something being published that
// cannot be unpublished - a real name or address in a fixture or a pasted
// transcript is public the moment the push lands, and no later push takes it
// back.
func heavy(t *testing.T, why string) {
	t.Helper()
	if why == "" {
		t.Fatal("heavy needs the reason this guard can wait for the pull request")
	}
	if testing.Short() {
		t.Skip("heavy, so it runs on the pull request rather than at push: " + why)
	}
}

// The skip above is honest only while something runs the heavy guards. It is
// the pull request: the test lanes run this package without -short, and this
// refuses a workflow that passes it, because a -short in CI would turn every
// heavy guard into a green skip with nothing left anywhere that runs it.
//
// The tier runs in two lanes, so "runs the whole package" is now two
// commands: the invariants lane runs `go test ./...` skipping the meta-tests,
// and the meta lane runs only them. Both name them through META_TESTS, and
// that is where this looks for the gap. A name in it that matches no test
// would be skipped by one lane and selected by nothing in the other - `-run`
// with no match passes with "no tests to run" - so every name must be a test
// that exists.
func TestHeavyGuardsRunOnEveryPullRequest(t *testing.T) {
	t.Parallel()
	var wf struct {
		Env  map[string]string `yaml:"env"`
		Jobs map[string]struct {
			Steps []struct {
				Run string `yaml:"run"`
				Dir string `yaml:"working-directory"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	repository(t).YAMLInto(t, workflowPath("pr-validation.yml"), &wf)

	full, skipsMeta, meta := false, false, false
	for _, job := range wf.Jobs {
		for _, step := range job.Steps {
			run := strings.TrimSpace(step.Run)
			if step.Dir == "tests/go" && (run == "go test ./..." || run == `go test ./... -skip "$META_TESTS"`) {
				full = true
				skipsMeta = skipsMeta || strings.Contains(run, "-skip")
			}
			meta = meta || run == `go test ./repo/ -run "$META_TESTS"`
			for _, line := range strings.Split(step.Run, "\n") {
				if strings.Contains(line, "go test") && strings.Contains(line, "-short") {
					t.Errorf("pr-validation.yml runs Go tests with -short: %s\n\n"+
						"-short is the push budget, and the pull request is where the heavy guards "+
						"it skips are supposed to run", strings.TrimSpace(line))
				}
			}
		}
	}
	if !full {
		t.Error("pr-validation.yml no longer runs `go test ./...` in tests/go, so the guards " +
			"heavy() skips at push run nowhere - every one of them is switched off")
	}
	if !full || !skipsMeta {
		return
	}

	// The full run skips the meta-tests, so they must run somewhere else,
	// selected by the same variable.
	if !meta {
		t.Error("pr-validation.yml skips $META_TESTS in the full run of tests/go and no lane " +
			"runs them with `go test ./repo/ -run \"$META_TESTS\"`, so the mutation ledger and " +
			"the comment-stripping re-run are switched off")
	}
	decl := regexp.MustCompile(`^\^\(([A-Za-z0-9_|]+)\)\$$`).FindStringSubmatch(wf.Env["META_TESTS"])
	if decl == nil {
		t.Fatal("pr-validation.yml uses $META_TESTS but does not declare it at the top as " +
			"META_TESTS: \"^(TestA|TestB)$\", so this cannot check that each name is a real test")
	}
	defined := map[string]bool{}
	for _, f := range repoTestFuncs(t) {
		defined[f] = true
	}
	for _, name := range strings.Split(decl[1], "|") {
		if !defined[name] {
			t.Errorf("META_TESTS names %s, which is not a test in tests/go/repo.\n\n"+
				"The invariants lane skips it and the meta lane's -run matches nothing, which "+
				"passes with \"no tests to run\". Rename it here to match the test.", name)
		}
	}
}

// repoTestFuncs lists every top-level test declared in this package.
func repoTestFuncs(t *testing.T) []string {
	t.Helper()
	tree := repository(t)
	fset := token.NewFileSet()
	var out []string
	for _, rel := range tree.Files(t) {
		if filepath.Dir(rel) != guardsDir || !strings.HasSuffix(rel, "_test.go") {
			continue
		}
		for _, d := range tree.Go(t, fset, rel).Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") && takesT(fn) {
				out = append(out, fn.Name.Name)
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("found no tests in tests/go/repo, so no name could be checked against them")
	}
	return out
}

// takesT reports whether a function's one parameter is a *testing.T.
func takesT(fn *ast.FuncDecl) bool {
	params := fn.Type.Params.List
	if len(params) != 1 || len(params[0].Names) > 1 {
		return false
	}
	star, ok := params[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "testing" && sel.Sel.Name == "T"
}

// Every guard that runs at push runs beside the others. The push ran 253
// guards one after another and took 27 seconds against a budget of ten; side
// by side they take under eight. A guard that is heavy is not at push, and
// keeps how it runs in the pull request: heavy suites side by side once held
// a millisecond Rego test past conftest's five seconds (#781). A guard that
// changes the process - its environment or where it runs - cannot run beside
// another, and Go refuses t.Setenv in one that tries.
func TestEveryGuardAtPushRunsBesideTheOthers(t *testing.T) {
	t.Parallel()
	tree := repository(t)
	fset := token.NewFileSet()
	read := 0
	for _, rel := range tree.Files(t) {
		if filepath.Dir(rel) != guardsDir || !strings.HasSuffix(rel, "_test.go") {
			continue
		}
		for _, d := range tree.Go(t, fset, rel).Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") || !takesT(fn) || fn.Body == nil {
				continue
			}
			read++
			if why := runsAlone(fn); why != "" {
				continue
			}
			t.Errorf("%s: %s runs at push and does not call t.Parallel(), so every guard after it waits. Call it first; a guard that cannot run beside others changes the process, and says so by doing it.", fset.Position(fn.Pos()), fn.Name.Name)
		}
	}
	if read < 100 {
		t.Fatalf("read only %d guards from %s, which is not this package", read, guardsDir)
	}
}

// runsAlone is why a test does not run beside the others - it is heavy, or it
// changes the process - or nothing when it should, or already does.
func runsAlone(fn *ast.FuncDecl) string {
	name := fn.Type.Params.List[0].Names[0].Name
	why := "no reason"
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch f := call.Fun.(type) {
		case *ast.Ident:
			if f.Name == "heavy" {
				why = "heavy"
			}
		case *ast.SelectorExpr:
			if id, ok := f.X.(*ast.Ident); ok && id.Name == name && f.Sel.Name == "Parallel" {
				why = "runs beside the others"
			}
			if processChanges[f.Sel.Name] {
				why = "changes the process"
			}
		}
		return true
	})
	if why == "no reason" {
		return ""
	}
	return why
}

// processChanges is what a test cannot do while another runs.
var processChanges = map[string]bool{"Setenv": true, "Unsetenv": true, "Clearenv": true, "Chdir": true}

// The reading, on tests made up here.
func TestAGuardIsSeenToRunBesideTheOthersOrToSayWhyNot(t *testing.T) {
	t.Parallel()
	for src, want := range map[string]string{
		"func TestA(t *testing.T) { t.Parallel() }":                        "runs beside the others",
		"func TestA(t *testing.T) { heavy(t, \"slow\") }":                  "heavy",
		"func TestA(t *testing.T) { t.Setenv(\"A\", \"b\") }":              "changes the process",
		"func TestA(t *testing.T) { if false { return }; doSomething(t) }": "",
		"func TestA(t *testing.T) { s := \"t.Parallel()\"; _ = s }":        "",
	} {
		f, err := parser.ParseFile(token.NewFileSet(), "a_test.go", "package a\n"+src, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := runsAlone(f.Decls[0].(*ast.FuncDecl)); got != want {
			t.Errorf("%s: got %q, want %q", src, got, want)
		}
	}
}
