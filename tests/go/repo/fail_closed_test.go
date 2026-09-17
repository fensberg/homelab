package repo

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// A guard that cannot ask its question must say so, not pass.
//
// THE RULE. "I looked and found nothing" and "I could not look" are different
// facts, and only one of them is reassuring. A guard that skips, or swallows
// the error from the thing it was about to inspect, reports the second as the
// first - and an exit code cannot tell them apart.
//
// This repository has removed that shape from a dozen individual places: a plan
// summarising "no changes" from a document it never parsed, a dry run exiting 0
// having matched no hosts, an approved-registries check reporting on images it
// never read. Each was found by somebody noticing. This is the version that
// does not need anybody to notice.
//
// WHY A META-GUARD RATHER THAN FIXING THE ONES THAT EXIST. Fixing them is a
// day's work that decays from the moment it is finished, because the next guard
// written is written by somebody who has not read this paragraph. The audit is
// worth doing once; what keeps it done is that a NEW fail-open guard cannot be
// added without declaring it, and a declaration is a line in a reviewed file
// with a reason attached.
//
// WHAT IT CANNOT CATCH, said plainly. It sees t.Skip, which is the explicit
// form. It does not see a guard that loops over an empty set, or one whose
// claim is wider than its reach - those are #384's other two questions, and
// TestEveryGuardThatDiscoversItsSubjectAssertsItFoundSome covers the first.
// Saying so is better than implying this closes the family.

// skipIsHonest names the guards permitted to skip, and why each one is not
// fail-open.
//
// The bar is that skipping reports something TRUE. A patch harness with no
// patches genuinely has nothing to check; a guard that cannot reach the thing
// it guards does not.
var skipIsHonest = map[string]string{
	"patches_test.go": "no patches outstanding is the NORMAL state, and it is a true " +
		"statement about the repository rather than a failure to look. " +
		"outstandingPatches has already read the directory by then.",

	"mutation_ledger_test.go": "the ledger runs each guard twice - once as the harness " +
		"and once as the subject - and the inner run must not recurse. The skip is " +
		"keyed on repoRootEnv, which only the harness sets.",

	"build_artifacts_test.go": "one skip, keyed on the same repoRootEnv: the ledger's " +
		"scratch tree is deliberately not a git repository, so there is no working " +
		"tree to ask git about. Every other git failure is a Fatal.",

	"heavy_test.go": "a guard that costs seconds skips only under -short, which is the " +
		"pre-push hook's budget, and says so with its reason. TestHeavyGuardsRunOnEveryPullRequest " +
		"refuses a CI run with -short, so the skip reports that the guard runs on the pull " +
		"request - which is true - and never that it runs nowhere.",

	"zizmor_test.go": "the exemption block being gone is a real outcome with a sibling " +
		"test that covers it - if the block has gone, the relative references should " +
		"have gone with it, and that is what the next test asserts.",

	// hermetic_tests_test.go is NOT here, and that is the point: it used to
	// skip, its own comment records replacing that with an assertion that the
	// walk found something, and the entry I wrote for it was refused by this
	// guard on its first run. A declaration outliving its reason is how an
	// exemption list becomes an allow list wearing a different hat.
}

func TestNoGuardSkipsWithoutSayingWhyItIsHonest(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "tests", "go", "repo")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	skipping := map[string]int{}
	examined := 0

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		if e.Name() == "fail_closed_test.go" {
			continue // this file names the call in order to refuse it
		}
		f, parseErr := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", e.Name(), parseErr)
		}
		examined++

		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch callName(call) {
			case "t.Skip", "t.Skipf", "t.SkipNow":
				skipping[e.Name()]++
			}
			return true
		})
	}

	const atLeastThirtyGuardFiles = 30
	if examined < atLeastThirtyGuardFiles {
		t.Fatalf(`only %d guard file(s) were parsed, and this package has far more.

The walk has stopped matching, so a guard could start skipping anywhere it no
longer looks - which is the same green as no guard skipping at all.`, examined)
	}

	var undeclared []string
	for file := range skipping {
		if reason, declared := skipIsHonest[file]; !declared || strings.TrimSpace(reason) == "" {
			undeclared = append(undeclared, file)
		}
	}
	sort.Strings(undeclared)

	if len(undeclared) > 0 {
		t.Errorf(`%d guard file(s) skip without a declared reason:

  %s

A skip is fail-open: the run is green, and nothing distinguishes "I looked and
found nothing" from "I could not look". That is the exact shape this repository
has had to repair in a plan summary, a dry run and a registry check, each time
after somebody noticed rather than because anything said so.

Two ways forward, and the first is usually right:

  - FAIL instead. If the guard cannot reach its subject, that is a finding
    about the environment and saying so is the point.
  - Declare it in skipIsHonest with a reason that says what TRUE thing the skip
    reports. "No patches are outstanding" is true about the repository; "git
    would not answer" is not.`, len(undeclared), strings.Join(undeclared, "\n  "))
	}

	// And a declaration for a file that no longer skips is stale - it makes the
	// exemption list longer than the truth and invites the next one.
	for file := range skipIsHonest {
		if skipping[file] == 0 {
			t.Errorf(`skipIsHonest declares %q and it no longer skips.

Remove the entry. An exemption outliving its reason is how a list of
well-reasoned exceptions becomes an allow list wearing a different hat.`, file)
		}
	}
}

// A guard that could not read its subject must not carry on as though it had.
//
// THE SECOND MECHANICAL FORM of the same rule. A skip is the explicit way to
// fail open; this is the quiet one:
//
//	if err != nil {
//	    continue        // <- the subject was never examined, and nothing says so
//	}
//
// The loop moves on, every later assertion runs against a smaller set than it
// claims to check, and the run is green. It is the same failure as a skip
// scoped to one subject instead of the whole guard, and it is harder to see
// because it looks like ordinary Go.
//
// WHAT IS NOT THIS. Propagating the error - `return err` from a walk callback -
// is fail-CLOSED: the caller gets it and fails. So is recording a failure and
// then continuing, which is how a guard reports every offender rather than
// stopping at the first. Both are common here and both are correct.
//
// So the rule is narrow and precise: an error from the subject must produce a
// failure, or be handed to somebody who will. Being dropped is the one thing
// it may not be.

// swallowsTheError reports whether an `if err != nil` block neither fails the
// test nor hands the error on.
func swallowsTheError(block *ast.BlockStmt) bool {
	swallowed := true
	ast.Inspect(block, func(n ast.Node) bool {
		if !swallowed {
			return false
		}
		switch node := n.(type) {
		case *ast.CallExpr:
			switch callName(node) {
			case "t.Error", "t.Errorf", "t.Fatal", "t.Fatalf", "t.Skip", "t.Skipf",
				"panic", "log.Fatal", "log.Fatalf":
				// Reported, or refused. Either way it is not dropped.
				swallowed = false
			}
		case *ast.ReturnStmt:
			// `return err` hands it up; `return` and `return nil` drop it.
			for _, r := range node.Results {
				if id, ok := r.(*ast.Ident); ok && id.Name == "nil" {
					continue
				}
				swallowed = false
			}
		}
		return swallowed
	})
	return swallowed
}

// looksLikeAnErrorCheck reports whether a condition tests an error for nil.
func looksLikeAnErrorCheck(cond ast.Expr) bool {
	found := false
	ast.Inspect(cond, func(n ast.Node) bool {
		cmp, ok := n.(*ast.BinaryExpr)
		if !ok || cmp.Op != token.NEQ {
			return true
		}
		left, leftOK := cmp.X.(*ast.Ident)
		right, rightOK := cmp.Y.(*ast.Ident)
		if leftOK && rightOK && right.Name == "nil" &&
			strings.Contains(strings.ToLower(left.Name), "err") {
			found = true
		}
		return !found
	})
	return found
}

// Error drops this repository has not yet closed, counted per file.
//
// A CEILING, NOT AN EXEMPTION LIST, and the difference is the whole point. A
// per-file reason would permit that file any number of drops forever; a COUNT
// permits exactly the ones that exist. Adding one fails until somebody writes
// the new number down, and removing one fails until they lower it - so the
// number can only go down, and a new guard cannot fail open quietly.
//
// This is the same shape as tests/coverage-exemptions.yml, for the same reason
// it was chosen there: a list of well-reasoned exceptions stops being a debt
// anybody intends to pay, and a number is harder to argue with than a
// paragraph.
//
// EMPTY IS THE TARGET. Each of these is a place where a guard could not read
// its subject and carried on as though it had, so every later assertion in that
// loop ran against a smaller set than it claims to check. Some will turn out to
// be honest - a tier directory that does not exist yet genuinely names no
// surfaces - and those want the drop replaced by an explicit check for that
// condition, so the honest case is stated rather than inferred from an error.
//
// Found by this guard on the day it was written: 25 across 13 files.
var droppedErrors = map[string]int{
	"build_artifacts_test.go":    2,
	"cni_test.go":                1,
	"duplicates_test.go":         1,
	"estate_surfaces_test.go":    2,
	"gitops_test.go":             1,
	"go_invocation_test.go":      4,
	"mutation_ledger_test.go":    6,
	"patches_test.go":            1,
	"pinned_endpoints_test.go":   2,
	"placeholder_config_test.go": 1,
	"recovery_paths_test.go":     1,
	"sensitivepaths_test.go":     1,
	"workload_placement_test.go": 3,
}

// The ceiling on the whole debt. It may only fall.
const droppedErrorCeiling = 26

func TestNoGuardDropsAnErrorFromItsOwnSubject(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "tests", "go", "repo")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	dropping := map[string][]int{}
	examined := 0

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") || e.Name() == "fail_closed_test.go" {
			continue
		}
		f, parseErr := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", e.Name(), parseErr)
		}
		examined++

		ast.Inspect(f, func(n ast.Node) bool {
			ifStmt, ok := n.(*ast.IfStmt)
			if !ok || !looksLikeAnErrorCheck(ifStmt.Cond) {
				return true
			}
			if swallowsTheError(ifStmt.Body) {
				dropping[e.Name()] = append(dropping[e.Name()], fset.Position(ifStmt.Pos()).Line)
			}
			return true
		})
	}

	const atLeastThirtyGuardFiles = 30
	if examined < atLeastThirtyGuardFiles {
		t.Fatalf("only %d guard file(s) were parsed, so this examined almost nothing", examined)
	}

	var findings []string
	total := 0
	for file, lines := range dropping {
		total += len(lines)
		allowed := droppedErrors[file]
		if len(lines) > allowed {
			for _, line := range lines[allowed:] {
				findings = append(findings, fmt.Sprintf("%s:%d", file, line))
			}
		}
	}
	sort.Strings(findings)

	if len(findings) > 0 {
		t.Errorf(`%d place(s) drop an error from the thing being guarded, beyond what
this repository has written down:

  %s

The loop moves on, every later assertion runs against a smaller set than it
claims to check, and the run is green. That is a skip scoped to one subject,
and it is harder to see because it looks like ordinary Go.

Three ways out, and the first two are the ones to reach for:

  - t.Errorf and carry on, so every offender is reported rather than the first.
  - return the error, so whoever called the walk fails.
  - state the honest case explicitly. "A tier directory that does not exist
    names no surfaces" is true about the repository - so check for the
    directory, rather than inferring it from any error at all.

Raising a number in droppedErrors is the last resort and it is visible: the
ceiling may only fall.`, len(findings), strings.Join(findings, "\n  "))
	}

	if total > droppedErrorCeiling {
		t.Errorf("%d error drops, and the ceiling is %d. It may only fall.", total, droppedErrorCeiling)
	}

	for file, allowed := range droppedErrors {
		if got := len(dropping[file]); got < allowed {
			t.Errorf(`%s drops %d error(s) and droppedErrors says %d.

Lower it. A number left above the truth is slack the next change can spend
without anybody noticing, which is how the coverage baseline came to sit ten
points below the real figure.`, file, got, allowed)
		}
	}
}
