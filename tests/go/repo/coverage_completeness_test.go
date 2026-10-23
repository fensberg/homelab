package repo

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// Coverage is discovered and fails closed.
//
// WHY THIS EXISTS, in the operator's words: "I do not trust myself to be a good
// reviewer and I do not trust you to be a good coder. We therefore must build
// safeguards in code to protect both parties from doing something stupid."
//
// The regime this replaces was an allow list. `task test:coverage` measured one
// Go module of seven; two modules were compiled and run by nothing at all, so
// every guard written for one of them had only ever passed in the terminal that
// wrote it; and three whole tiers - shell, Ansible, OpenTofu - sat outside it
// with nothing saying so. Nobody removed those checks. They were never added,
// and **an allow list cannot tell the difference between a check that is absent
// and a check that is fine**.
//
// So the units are enumerated from the filesystem, not from a list. A unit that
// nothing executes fails, unless it is named in tests/coverage-blocklist.yml -
// which is a debt with a ceiling that may only fall, not an exemption with a
// reason.
//
// TESTED MEANS EXECUTED, NOT READ. A test that greps a script's text is a
// change detector: it passes forever while the behaviour rots. This repository
// refuses those by name, and six of its seven shell scripts were in exactly
// that state - including the gate that enforces coverage, the script that sets
// up every machine, and all three git hooks.

type coverageBlocklist struct {
	Ceiling int      `yaml:"ceiling"`
	Blocked []string `yaml:"blocked"`
}

// A unit is "<tier>:<path>", so one flat list covers every tier and a typo in
// the tier is as visible as a typo in the path.
type unit struct {
	tier, path string
}

func (u unit) String() string { return u.tier + ":" + u.path }

// executesIt reports whether any test in the repository RUNS the given path,
// rather than reading it: whether a string a test starts a program with - in
// the call that starts it, or in a call to a function of the same package
// that does - names it. The word boundaries are load-bearing: without one,
// `sh` matched the tail of "push" and "finish", so a sentence of prose about
// the pre-push hook counted as running it. And prose is no longer read at
// all: these are the strings of the calls, from Go's parser.
func executesIt(run []string, path string) bool {
	named := regexp.MustCompile(`\b` + regexp.QuoteMeta(filepath.Base(path)) + `\b`)
	return slices.ContainsFunc(run, named.MatchString)
}

// programStrings is every string, written out or named by a constant, in a
// call a Go test starts a program with, or in a call to a function of its
// package that starts one.
func programStrings(t *testing.T) []string {
	t.Helper()
	tree := repository(t)
	byPackage := map[string][]*ast.File{}
	fset := token.NewFileSet()
	for _, rel := range tracked(t, func(rel string) bool { return strings.HasSuffix(rel, "_test.go") }) {
		byPackage[filepath.Dir(rel)] = append(byPackage[filepath.Dir(rel)], tree.Go(t, fset, rel))
	}
	starts := func(call *ast.CallExpr) bool {
		switch callName(call) {
		case "exec.Command", "exec.CommandContext":
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		return ok && (sel.Sel.Name == "Invoke" || sel.Sel.Name == "InvokeBinary")
	}
	calledName := func(call *ast.CallExpr) string {
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			return fun.Name
		case *ast.SelectorExpr:
			return fun.Sel.Name
		}
		return ""
	}
	var out []string
	for _, files := range byPackage {
		consts := map[string]*ast.File{}
		for i, f := range files {
			consts[strconv.Itoa(i)] = f
		}
		values := stringConstants(consts)
		// The functions that start a program, and those that call them.
		runners := map[string]bool{}
		for range 2 {
			for _, f := range files {
				for _, d := range f.Decls {
					fn, ok := d.(*ast.FuncDecl)
					if !ok || fn.Body == nil {
						continue
					}
					ast.Inspect(fn.Body, func(n ast.Node) bool {
						if call, ok := n.(*ast.CallExpr); ok && (starts(call) || runners[calledName(call)]) {
							runners[fn.Name.Name] = true
						}
						return true
					})
				}
			}
		}
		for _, f := range files {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !starts(call) && !runners[calledName(call)] {
					return true
				}
				for _, arg := range call.Args {
					ast.Inspect(arg, func(n ast.Node) bool {
						switch n := n.(type) {
						case *ast.BasicLit:
							if v, ok := stringLiteral(n); ok {
								out = append(out, v)
							}
						case *ast.Ident:
							if v, ok := values[n.Name]; ok {
								out = append(out, v)
							}
						}
						return true
					})
				}
				return true
			})
		}
	}
	if len(out) == 0 {
		t.Fatal("found no test that starts a program at all, so every unit would look unrun")
	}
	return out
}

// tofuTests is every OpenTofu test file in the repository, parsed.
func tofuTests(t *testing.T) map[string]*hclsyntax.Body {
	t.Helper()
	tree := repository(t)
	out := map[string]*hclsyntax.Body{}
	for _, rel := range trackedMatching(t, func(rel string) bool {
		return authoredHere(t, rel) && strings.HasSuffix(rel, ".tftest.hcl")
	}) {
		body, ok := tree.HCL(t, rel).Body.(*hclsyntax.Body)
		if !ok {
			t.Fatalf("%s is not OpenTofu's native syntax", rel)
		}
		out[rel] = body
	}
	if len(out) == 0 {
		t.Fatal("found no OpenTofu test files at all, so every OpenTofu unit would look uncovered")
	}
	return out
}

// tracked lists this repository's own files matching a predicate.
//
// It used to walk from the root with a skipDirs map, because the mutation
// ledger's scratch tree was not a git repository and `git ls-files` there
// exited 128. The ledger runs `git init` in that copy now, so this is the same
// enumeration everything else uses.
func tracked(t *testing.T, keep func(rel string) bool) []string {
	t.Helper()
	return trackedMatching(t, func(rel string) bool {
		return authoredHere(t, rel) && keep(rel)
	})
}

// discover enumerates every unit this repository is expected to test.
func discover(t *testing.T) []unit {
	t.Helper()
	tree := repository(t)
	var units []unit

	// Go: one unit per module under scripts/. tests/go is test code, and
	// measuring how thoroughly the tests test themselves is a number with no
	// meaning - the coverage baseline has said so since it was written.
	for _, m := range goModules(t) {
		units = append(units, unit{"go", "scripts/" + m})
	}

	// Shell: every script the repository ships, wherever it lives. A code
	// rule's example is not one: it is written to be refused, and read - never
	// run - by semgrep --test (TestEveryCodeRuleProvesItself).
	for _, p := range tracked(t, func(rel string) bool {
		return (strings.HasSuffix(rel, ".sh") || strings.HasPrefix(rel, "githooks/")) && !strings.HasPrefix(rel, semgrepExamples+"/")
	}) {
		units = append(units, unit{"shell", p})
	}

	// Ansible: a YAML that actually plays something. requirements.yml declares
	// collections and executes nothing, so it is not a unit.
	for _, p := range tracked(t, func(rel string) bool {
		return strings.HasPrefix(rel, "management/hypervisor/") && strings.HasSuffix(rel, ".yml")
	}) {
		if plays(tree.YAMLDocuments(t, p)) {
			units = append(units, unit{"ansible", p})
		}
	}

	// OpenTofu: a file is a unit when it asserts something. A file that only
	// declares resources is exercised by any plan; a file with a validation or
	// a precondition is making a claim that something should assert fails.
	for _, p := range tracked(t, func(rel string) bool { return strings.HasSuffix(rel, ".tf") }) {
		if body, ok := tree.HCL(t, p).Body.(*hclsyntax.Body); ok && refuses(body) {
			units = append(units, unit{"tofu", p})
		}
	}

	return units
}

// plays says whether a YAML file plays something: a play has hosts or tasks.
func plays(docs []any) bool {
	for _, d := range docs {
		list, _ := d.([]any)
		for _, item := range list {
			if play, ok := item.(map[string]any); ok && (play["hosts"] != nil || play["tasks"] != nil) {
				return true
			}
		}
	}
	return false
}

// refuses says whether anything in a body refuses something: a validation,
// a precondition, a postcondition, or a check.
func refuses(n hclsyntax.Node) bool {
	found := false
	_ = hclsyntax.VisitAll(n, func(n hclsyntax.Node) hcl.Diagnostics {
		if b, ok := n.(*hclsyntax.Block); ok && refusing[b.Type] {
			found = true
		}
		return nil
	})
	return found
}

// covered decides whether a unit is executed by something in the test suite.
func covered(t *testing.T, u unit, tests map[string]*hclsyntax.Body, run []string, floors map[string]float64) bool {
	t.Helper()
	switch u.tier {
	case "go":
		// Run by `task test:go`'s sweep, and its depth is held by a floor.
		// TestEveryGoModuleHasACoverageFloor owns the second half.
		_, hasFloor := floors["go/"+filepath.Base(u.path)]
		return hasFloor

	case "shell", "ansible":
		return executesIt(run, u.path)

	case "tofu":
		// Exercised when a .tftest.hcl expects one of its declared identifiers
		// to fail. A plan alone runs the file; only expect_failures proves the
		// assertion was ever reached.
		body, ok := repository(t).HCL(t, u.path).Body.(*hclsyntax.Body)
		if !ok {
			t.Fatalf("%s is not OpenTofu's native syntax", u.path)
		}
		// Every declaration in the file that refuses something, and each
		// has to be one a test of this root expects to fail. Read from the
		// tests' expect_failures lists, not from their text: a test that
		// merely mentions a name, or lists nothing, has not seen it refuse.
		expected := map[string]bool{}
		for name, test := range tests {
			// Its own root's tests, and no other's. A test runs the root
			// it sits in, so one in another root that happens to name the
			// same identifier never reached this file's assertion - which
			// is how a second root's `variable "site"` validation counted
			// as exercised by the first root's test of its own.
			if testsTheRootOf(name, u.path) {
				for _, addr := range expectedFailures(test) {
					expected[addr] = true
				}
			}
		}
		asserting := assertingDeclarations(body)
		for _, addr := range asserting {
			if !expected[addr] {
				return false
			}
		}
		return len(asserting) > 0
	}
	t.Fatalf("unit %s has a tier nothing knows how to check", u)
	return false
}

// assertingDeclarations is the address of every top-level block in an
// OpenTofu file that refuses something - a validation, a precondition, a
// postcondition, or a check - as a test names it in expect_failures.
func assertingDeclarations(body *hclsyntax.Body) []string {
	var out []string
	for _, b := range body.Blocks {
		if b.Type != "check" && !refuses(b.Body) {
			continue
		}
		switch {
		case b.Type == "variable" && len(b.Labels) == 1:
			out = append(out, "var."+b.Labels[0])
		case (b.Type == "output" || b.Type == "check") && len(b.Labels) == 1:
			out = append(out, b.Type+"."+b.Labels[0])
		case b.Type == "resource" && len(b.Labels) == 2:
			out = append(out, b.Labels[0]+"."+b.Labels[1])
		case b.Type == "data" && len(b.Labels) == 2:
			out = append(out, "data."+b.Labels[0]+"."+b.Labels[1])
		}
	}
	sort.Strings(out)
	return out
}

// expectedFailures is every address a test file's runs list in their
// expect_failures, in the order written.
func expectedFailures(test *hclsyntax.Body) []string {
	var out []string
	for _, b := range test.Blocks {
		attr, ok := b.Body.Attributes["expect_failures"]
		if b.Type != "run" || !ok {
			continue
		}
		list, ok := attr.Expr.(*hclsyntax.TupleConsExpr)
		if !ok {
			continue
		}
		for _, e := range list.Exprs {
			if ref, ok := e.(*hclsyntax.ScopeTraversalExpr); ok {
				out = append(out, traversalName(ref.Traversal))
			}
		}
	}
	return out
}

// testsTheRootOf reports whether a .tftest.hcl file runs the root or module an
// OpenTofu file belongs to: the test sits in that directory, or in the tests/
// directory directly beneath it.
func testsTheRootOf(test, file string) bool {
	dir := filepath.Dir(test)
	if filepath.Base(dir) == "tests" {
		dir = filepath.Dir(dir)
	}
	return filepath.ToSlash(dir) == filepath.ToSlash(filepath.Dir(file))
}

// blocklistEntries is the declared debt, shared by every guard that can defer
// something into it. One list and one ceiling, so the total debt is a number
// somebody can look at rather than a set of separate allowances.
func blocklistEntries(t *testing.T) map[string]bool {
	t.Helper()
	var list coverageBlocklist
	repository(t).YAMLInto(t, "tests/coverage-blocklist.yml", &list)
	out := map[string]bool{}
	for _, b := range list.Blocked {
		out[strings.TrimSpace(b)] = true
	}
	return out
}

func TestEveryUnitIsExecutedByATestOrIsADeclaredDebt(t *testing.T) {
	tree := repository(t)
	var list coverageBlocklist
	tree.YAMLInto(t, "tests/coverage-blocklist.yml", &list)

	var floors struct {
		Baselines map[string]float64 `json:"baselines"`
	}
	tree.JSONInto(t, "tests/coverage-baseline.json", &floors)

	blocked := map[string]bool{}
	for _, b := range list.Blocked {
		blocked[strings.TrimSpace(b)] = true
	}

	// The ceiling may only fall, and it is the count rather than a number
	// somebody keeps in step by hand.
	if len(list.Blocked) > list.Ceiling {
		t.Errorf(`tests/coverage-blocklist.yml has %d entries and a ceiling of %d.

The ceiling may only fall. Adding an entry means something stopped being
tested, or arrived untested - either way it is a debt and it does not get to
raise its own limit.`, len(list.Blocked), list.Ceiling)
	}

	tests := tofuTests(t)
	run := programStrings(t)
	units := discover(t)
	if len(units) == 0 {
		t.Fatal("discovered no units at all, so this asserts nothing")
	}

	var uncovered, stale []string
	for _, u := range units {
		isCovered := covered(t, u, tests, run, floors.Baselines)
		switch {
		case isCovered && blocked[u.String()]:
			stale = append(stale, u.String())
		case !isCovered && !blocked[u.String()]:
			uncovered = append(uncovered, u.String())
		}
	}
	sort.Strings(uncovered)
	sort.Strings(stale)

	if len(uncovered) > 0 {
		t.Errorf(`%d unit(s) are executed by no test and are not declared as debt:

  %s

Write a test that RUNS it - reading its source is a change detector, which
passes forever while the behaviour rots. If it genuinely cannot be tested yet,
add it to tests/coverage-blocklist.yml and raise the ceiling in the same
commit, which makes the debt visible instead of invisible.`,
			len(uncovered), strings.Join(uncovered, "\n  "))
	}

	if len(stale) > 0 {
		t.Errorf(`%d unit(s) are on the block list and are now tested:

  %s

Remove them and lower the ceiling by the same number. A block list that keeps
entries it no longer needs is slack, and slack is how a coverage baseline here
once sat ten points below the real figure without anybody noticing.`,
			len(stale), strings.Join(stale, "\n  "))
	}
}

func TestAssertingDeclarationsAndExpectedFailuresAreReadNotMatched(t *testing.T) {
	body := `variable "site" {
  type = string
  validation {
    condition = true
  }
}

variable "plain" {
  type = string
}

output "grants" {
  value = 1
  precondition {
    condition = true
  }
}

resource "terraform_data" "invariants" {
  lifecycle {
    precondition {
      condition = true
    }
  }
}

resource "terraform_data" "quiet" {
  # validation { would be here }
}

data "thing" "read" {
  lifecycle {
    postcondition {
      condition = true
    }
  }
}

check "reachable" {
}
`
	want := []string{"check.reachable", "data.thing.read", "output.grants", "terraform_data.invariants", "var.site"}
	parse := func(src string) *hclsyntax.Body {
		f, diags := hclsyntax.ParseConfig([]byte(src), "made-up.tf", hcl.InitialPos)
		if diags.HasErrors() {
			t.Fatal(diags.Error())
		}
		return f.Body.(*hclsyntax.Body)
	}
	if got := assertingDeclarations(parse(body)); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("asserting declarations are %v, want %v", got, want)
	}

	test := `run "a" {
  expect_failures = [var.site]
}
run "b" {
  # expect_failures = [var.commented]
  expect_failures = [
    terraform_data.invariants,
    output.grants,
  ]
}
run "c" {
  expect_failures = []
}
# a run that mentions var.plain and the word expect_failures expects nothing
`
	wantExpected := []string{"var.site", "terraform_data.invariants", "output.grants"}
	if got := expectedFailures(parse(test)); strings.Join(got, " ") != strings.Join(wantExpected, " ") {
		t.Errorf("expected failures are %v, want %v", got, wantExpected)
	}
}

// stringConstants is every string constant the package declares, by name, so
// a program named by a constant is named.
func stringConstants(files map[string]*ast.File) map[string]string {
	out := map[string]string{}
	for _, f := range files {
		for _, d := range f.Decls {
			gen, ok := d.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i < len(vs.Values) {
						if v, ok := stringLiteral(vs.Values[i]); ok {
							out[name.Name] = v
						}
					}
				}
			}
		}
	}
	return out
}

func stringLiteral(e ast.Expr) (string, bool) {
	lit, ok := ast.Unparen(e).(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	return v, err == nil
}
