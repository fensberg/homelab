package repo

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A test package that shells out to git must cut itself off from the
// developer's git configuration.
//
// Two tests in scripts/security were written without doing so, in consecutive
// changes. One built a deliberately unsigned commit and inherited
// `commit.gpgsign` from a global config, so the fixture was signed and the test
// failed on any machine set up the way this repository tells people to set one
// up. Both reported the machine rather than the code, and one of them surfaced
// on a workstation in the middle of a push.
//
// GIT_CONFIG_GLOBAL and GIT_CONFIG_SYSTEM pointed at /dev/null in a TestMain
// make the fixtures depend on what the tests set and nothing else.
// Per-repository config still applies, which is right - the fixture sets that
// itself.
//
// This is a check rather than a note because the failure is invisible on the
// machine that writes it. It only appears on a differently-configured one,
// which by then is somebody else's afternoon.
func TestPackagesThatShellToGitNeutraliseUserConfig(t *testing.T) {
	tree := repository(t)
	fset := token.NewFileSet()

	// package directory -> whether it invokes git, and whether it neutralises
	invokesGit := map[string]bool{}
	neutralises := map[string]bool{}
	visited := 0

	for _, rel := range tree.Files(t) {
		if !strings.HasSuffix(rel, "_test.go") || !strings.HasPrefix(rel, "scripts/") && !strings.HasPrefix(rel, "tests/go/") {
			continue
		}
		dir := filepath.Dir(rel)
		visited++
		ast.Inspect(tree.Go(t, fset, rel), func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				// Shelling out to git, by any of the shapes used here: the
				// program is the first argument of exec.Command, and the
				// second of exec.CommandContext and of the tree's Invoke.
				if program, ok := programOf(n); ok && program == "git" {
					invokesGit[dir] = true
				}
				// gitenv.Isolate is the one place that does it.
				if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Isolate" {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == "gitenv" {
						neutralises[dir] = true
					}
				}
			case *ast.BasicLit:
				// Setting the variables directly is still accepted.
				if v, err := strconv.Unquote(n.Value); err == nil && n.Kind == token.STRING && strings.Contains(v, "GIT_CONFIG_GLOBAL") {
					neutralises[dir] = true
				}
			}
			return true
		})
	}

	// Prove the walk worked before drawing any conclusion from what it found.
	//
	// This used to go straight to `t.Skip("no test package invokes git")`, and
	// a skip is the wrong answer twice over. It reports as neither pass nor
	// fail, so CI treats it as fine; and it cannot tell "no package invokes
	// git" - a real, checkable statement - from "this walk read no test files
	// at all", which means the guard is off. A rename or a move under either
	// root produces the second, permanently and in silence, and this is the
	// guard that catches a failure mode CLAUDE.md records as having already
	// happened twice.
	//
	// With a floor underneath it, an empty invokesGit genuinely means no
	// package shells out to git, which is a pass rather than a skip.
	if visited < 40 {
		t.Fatalf("only %d test files were read under scripts/ and tests/go/, which cannot be right - this guard is walking the wrong tree and proves nothing", visited)
	}
	if len(invokesGit) == 0 {
		return // Checked, and nothing shells out to git.
	}

	for dir := range invokesGit {
		if neutralises[dir] {
			continue
		}
		t.Errorf("%s runs git in its tests and does not neutralise the developer's "+
			"git configuration.\n\n"+
			"Add a TestMain that calls gitenv.Isolate() (homelab/details/gitenv). Without it a fixture inherits whatever the machine has - "+
			"commit.gpgsign is the one that has already caused this - and the test "+
			"reports the machine rather than the code.", dir)
	}
}

// programOf is the program a call runs, when it is one of the ways a test
// here starts one and the program is written as a literal.
func programOf(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	at := -1
	switch sel.Sel.Name {
	case "Command":
		at = 0
	case "CommandContext", "Invoke":
		at = 1
	}
	if at < 0 || len(call.Args) <= at {
		return "", false
	}
	lit, ok := call.Args[at].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	return v, err == nil
}
