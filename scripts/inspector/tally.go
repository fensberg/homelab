package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// What a change takes away, counted.
//
// Everything in tests/go/repo asserts a property of the repository AFTER a
// change. Nothing asserts a property OF the change. Those catch different
// things: a result-shaped check catches a known failure - the thing that
// should be there is not - and cannot catch the novel one, because nobody has
// written a rule for it yet.
//
// This counts removals instead, which is a class rather than an instance. "This
// pull request removes four test functions" is true whether or not anybody had
// registered those functions anywhere, and it is the sentence that would have
// made an overwritten test file visible inside a diff of 1,372 added lines.
//
// It reports and does not refuse. A removal is usually correct - work gets
// deleted, tests get replaced - so a check that blocked one would be wrong more
// often than right, and a check that is usually wrong gets ignored and then
// gets removed. The value is that this is stated where somebody is already
// looking, rather than inferred from a large diff.

type change struct {
	status string // git's name-status letter
	path   string
}

// report is what one change took away, and what it newly leans on.
type report struct {
	// newTools are binaries this change starts invoking and that the runner
	// image does not appear to install.
	//
	// The failure this catches, exactly: the Health phase began shelling out
	// to talosctl, and the hand-written list in the guard whose name promises
	// EVERY binary was not updated (#206). A list that has to be remembered
	// will eventually not be. Read off the change instead, so the thing that
	// introduces the dependency is the thing that raises it.
	newTools []string

	files []string          // deleted outright, under the watched roots
	tests map[string]string // test function -> the file it was in
	// assertions is the net change per package. Negative means fewer.
	assertions map[string]int
}

// watched are the roots where a deletion is worth saying out loud.
//
// Not everywhere: deleting a document or a manifest is ordinary. These three
// are where the building code, the automation and the programs live, which is
// where a quiet removal is expensive and where nobody reads every line.
var watched = []string{"tests/", ".github/", "scripts/"}

func gitOut(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// changed lists what moved between two commits.
func changed(root, base, head string) ([]change, error) {
	// -M so a rename is reported as one, not as a delete plus an add. A file
	// that moved has not lost anything, and saying it did is the kind of noise
	// that gets a report ignored.
	out, err := gitOut(root, "diff", "--name-status", "-M", "-z", base, head)
	if err != nil {
		return nil, err
	}

	// -z gives NUL-separated fields, status and path alternating. Renames
	// carry two paths; treated as a delete of the old name and an add of the
	// new, which is what a reader wants to be told.
	parts := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	var out2 []change
	for i := 0; i < len(parts); i++ {
		if parts[i] == "" {
			continue
		}
		status := parts[i]
		if i+1 >= len(parts) {
			break
		}
		i++
		out2 = append(out2, change{status: status[:1], path: parts[i]})
		if status[0] == 'R' && i+1 < len(parts) {
			i++
			out2 = append(out2, change{status: "R", path: parts[i]})
		}
	}
	return out2, nil
}

// testsIn returns the names of every Go test function in a source file.
//
// Parsed rather than matched. A regular expression for `func Test` also finds
// the one inside a string, the one in a comment explaining a test, and misses
// nothing usefully - and this number is the headline of the report, so it had
// better be right.
func testsIn(source string) []string {
	tree, err := parser.ParseFile(token.NewFileSet(), "x.go", source, 0)
	if err != nil {
		return nil
	}
	var names []string
	for _, d := range tree.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		if strings.HasPrefix(fn.Name.Name, "Test") || strings.HasPrefix(fn.Name.Name, "Fuzz") {
			names = append(names, fn.Name.Name)
		}
	}
	return names
}

// assertionsIn counts the calls that can actually fail a test.
//
// A test that keeps its name and loses its assertions still passes, and still
// reports coverage that no longer exists. Counting t.Error and t.Fatal is a
// blunt proxy for "how much can go red here", and blunt is enough: the report
// says the number moved, and a human decides whether that was the intention.
func assertionsIn(source string) int {
	tree, err := parser.ParseFile(token.NewFileSet(), "x.go", source, 0)
	if err != nil {
		return 0
	}
	var n int
	ast.Inspect(tree, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "Error", "Errorf", "Fatal", "Fatalf", "Skip", "Skipf":
			n++
		}
		return true
	})
	return n
}

// take compares two commits and reports what the later one no longer has.
func take(root, base, head string) (*report, error) {
	files, err := changed(root, base, head)
	if err != nil {
		return nil, err
	}

	r := &report{tests: map[string]string{}, assertions: map[string]int{}}

	// Tests are tracked across the whole change, not per file.
	//
	// The question is whether a guard left the REPOSITORY, not whether it left
	// one file. Asked per file, moving a test from a_test.go to b_test.go
	// reports both of its tests as removed - which is false, and is exactly
	// the sort of false alarm that gets a report ignored and then deleted.
	before := map[string]string{} // test name -> the file it was in
	after := map[string]bool{}
	toolsBefore, toolsAfter := map[string]bool{}, map[string]bool{}

	for _, c := range files {
		if c.status == "D" && underWatched(c.path) {
			r.files = append(r.files, c.path)
		}
		if !strings.HasSuffix(c.path, ".go") {
			continue
		}

		// A missing side is an empty side: an added file has no base, a
		// deleted one has no head. git's error here is not fatal to the
		// report - the file simply did not exist there.
		wasBody, _ := gitOut(root, "show", base+":"+c.path)
		isBody, _ := gitOut(root, "show", head+":"+c.path)

		for _, n := range testsIn(wasBody) {
			before[n] = c.path
		}
		for _, n := range testsIn(isBody) {
			after[n] = true
		}

		// Tools, like tests, are tracked across the whole change rather than
		// per file: moving an invocation from one file to another introduces
		// no new dependency, and reporting one would be the false alarm that
		// gets a report ignored.
		for _, tool := range toolsIn(wasBody) {
			toolsBefore[tool] = true
		}
		for _, tool := range toolsIn(isBody) {
			toolsAfter[tool] = true
		}

		if delta := assertionsIn(isBody) - assertionsIn(wasBody); delta != 0 {
			r.assertions[pkgOf(c.path)] += delta
		}
	}

	for name, path := range before {
		if !after[name] {
			r.tests[name] = path
		}
	}

	// A tool the change starts invoking, that the image does not appear to
	// carry. Read from the head Dockerfile rather than the working tree, so
	// the answer is about the change being reviewed.
	dockerfile, err := gitOut(root, "show", head+":.github/runner-image/Dockerfile")
	if err != nil {
		// No Dockerfile at head is not a finding, and is not a failure either.
		dockerfile = ""
	}
	for tool := range toolsAfter {
		if toolsBefore[tool] || alwaysPresent[tool] {
			continue
		}
		if dockerfile != "" && strings.Contains(dockerfile, tool) {
			continue
		}
		r.newTools = append(r.newTools, tool)
	}
	sort.Strings(r.newTools)

	sort.Strings(r.files)
	return r, nil
}

// alwaysPresent are on any runner without anybody installing them, so naming
// them would be noise rather than a finding.
var alwaysPresent = map[string]bool{
	"git": true, "sh": true, "bash": true, "env": true, "go": true,
}

func underWatched(path string) bool {
	for _, w := range watched {
		if strings.HasPrefix(path, w) {
			return true
		}
	}
	return false
}

func pkgOf(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[:i]
	}
	return "."
}

// command is a bare executable name, as opposed to a flag, a path or an
// environment assignment.
var command = regexp.MustCompile(`^[a-z][a-z0-9._-]*$`)

// toolsIn names the external binaries a source file invokes.
//
// Every way this repository starts a subprocess passes the executable as a
// plain string: exec.Command and exec.CommandContext take it first, and the
// run.Cmd* helpers all take `name string` immediately before their variadic
// args. So the rule is one thing rather than a table of argument positions -
// the first string literal in the call that looks like a bare command name.
//
// That skips the env slice a couple of helpers take second, because an entry
// there is `TALOSCONFIG=...` and carries an `=`; it skips flags, which start
// with a dash; and it skips paths, which carry a slash.
//
// It is deliberately not exhaustive and the report says so. A binary named by
// a variable cannot be read off the source, so this finds what it finds. Every
// one it does find is checked, which is strictly more than was checked before.
func toolsIn(source string) []string {
	tree, err := parser.ParseFile(token.NewFileSet(), "x.go", source, 0)
	if err != nil {
		return nil
	}

	seen := map[string]bool{}
	ast.Inspect(tree, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if !strings.HasPrefix(sel.Sel.Name, "Cmd") && !strings.HasPrefix(sel.Sel.Name, "Command") {
			return true
		}
		if name, ok := firstCommandLiteral(call.Args); ok {
			seen[name] = true
		}
		return true
	})

	var names []string
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func firstCommandLiteral(args []ast.Expr) (string, bool) {
	var found string
	var ok bool
	for _, a := range args {
		ast.Inspect(a, func(n ast.Node) bool {
			if ok {
				return false
			}
			lit, isLit := n.(*ast.BasicLit)
			if !isLit || lit.Kind != token.STRING {
				return true
			}
			v := strings.Trim(lit.Value, "`\"")
			if command.MatchString(v) {
				found, ok = v, true
				return false
			}
			return true
		})
		if ok {
			return found, true
		}
	}
	return "", false
}
