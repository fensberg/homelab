package repo

import (
	"go/ast"
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
// The tier runs in several lanes, so "runs the whole package" is several
// commands: the invariants lane runs `go test ./...` skipping the meta-tests
// and the guards with lanes of their own, and those lanes run only them. Each
// set is named once, in a variable at the top of the workflow (META_TESTS,
// OWN_LANE_TESTS), and that is where this looks for the gap. A name in it that matches no test
// would be skipped by one lane and selected by nothing in the other - `-run`
// with no match passes with "no tests to run" - so every name must be a test
// that exists.
func TestHeavyGuardsRunOnEveryPullRequest(t *testing.T) {
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

	// The full run, and the variables it skips by: `-skip "$META_TESTS"` or
	// `-skip "$META_TESTS|$OWN_LANE_TESTS"`. Each one names tests some other
	// lane runs with `go test ./repo/ -run "$NAME"`.
	fullRun := regexp.MustCompile(`^go test \./\.\.\.(?: -skip "((?:\$[A-Z_]+\|)*\$[A-Z_]+)")?$`)
	full := false
	var skipped []string
	ranBy := map[string]bool{}
	for _, job := range wf.Jobs {
		for _, step := range job.Steps {
			run := strings.TrimSpace(step.Run)
			if m := fullRun.FindStringSubmatch(run); step.Dir == "tests/go" && m != nil {
				full = true
				for _, v := range strings.Split(m[1], "|") {
					if v != "" {
						skipped = append(skipped, strings.TrimPrefix(v, "$"))
					}
				}
			}
			if v, ok := strings.CutPrefix(run, `go test ./repo/ -run "$`); ok && strings.HasSuffix(v, `"`) {
				ranBy[strings.TrimSuffix(v, `"`)] = true
			}
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
		return
	}

	// What the full run skips must run somewhere else, selected by the same
	// variable, and every name in it must be a test: `-run` with no match
	// passes with "no tests to run".
	defined := map[string]bool{}
	for _, f := range repoTestFuncs(t) {
		defined[f] = true
	}
	for _, name := range skipped {
		if !ranBy[name] {
			t.Errorf("pr-validation.yml skips $%s in the full run of tests/go and no lane "+
				"runs them with `go test ./repo/ -run \"$%s\"`, so they run nowhere", name, name)
		}
		decl := regexp.MustCompile(`^\^\(([A-Za-z0-9_|]+)\)\$$`).FindStringSubmatch(wf.Env[name])
		if decl == nil {
			t.Errorf("pr-validation.yml uses $%s but does not declare it at the top as "+
				"%s: \"^(TestA|TestB)$\", so this cannot check that each name is a real test", name, name)
			continue
		}
		for _, test := range strings.Split(decl[1], "|") {
			if !defined[test] {
				t.Errorf("%s names %s, which is not a test in tests/go/repo.\n\n"+
					"The invariants lane skips it and the lane's -run matches nothing, which "+
					"passes with \"no tests to run\". Rename it here to match the test.", name, test)
			}
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
