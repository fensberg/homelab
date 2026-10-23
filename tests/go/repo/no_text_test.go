package repo

import (
	"go/ast"
	"go/token"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"homelab/tests/parsed"
)

// No guard reads a file of the repository except through tests/parsed.
//
// WHY THIS EXISTS. The guards here read the repository every way there was:
// as text with a regular expression, through a helper that handed back a
// file's bytes, with a walk from a root found three different ways. Moving
// them onto parsers took a refactor nobody wants to repeat, and the way back
// to text is one line in a hurry. So the root belongs to tests/parsed, which
// hands out readers - each kind by a real parser - and no path; the guards
// start where no relative path reaches the repository (parsed.Main); and this
// refuses every other way a guard could find the repository or read it as
// text. A check of the repository's own files goes through that reader, or it
// is not written.
//
// What it refuses, in every file of this package:
//   - the packages that find the root, or read the repository from one:
//     repopath and tofufiles;
//   - the calls that find where a test runs or where it was built from;
//   - reading the variable that names the tree being judged;
//   - a file function given a relative path, or one that climbs;
//   - starting a program the source does not name, or one that is not go,
//     git, bash or a tool the suppliers list delivers - and in a script a
//     guard writes out for bash, a program that reads text; and git's verbs
//     that print a file.
//
// The ledger cannot prove it - an edit to Go source never reaches the binary
// the guards run from - so each way is proved by its table,
// TestTextReadsFindsEveryWayAGuardCouldReadTheRepository.
func TestNoGuardReadsAFileAsText(t *testing.T) {
	tree := repository(t)
	programs := deliveredPrograms(t, tree)
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, rel := range tree.Files(t) {
		if filepath.Dir(rel) == guardsDir && strings.HasSuffix(rel, ".go") {
			files[rel] = tree.Go(t, fset, rel)
		}
	}
	const atLeastFiftyGuardFiles = 50
	if len(files) < atLeastFiftyGuardFiles {
		t.Fatalf("only %d guard file(s) were read, and this package has far more", len(files))
	}
	consts := stringConstants(files)
	var problems []string
	for rel, f := range files {
		for _, p := range textReads(fset, f, programs, consts) {
			problems = append(problems, rel+":"+p)
		}
	}
	slices.Sort(problems)
	for _, p := range problems {
		t.Error(p + "\n\nRead the repository through tests/parsed: a Tree's readers, its files, and Invoke. If no reader fits, the reader is what to add, with its proof.")
	}
}

// deliveredPrograms is every program a guard may start: go, git and bash, and
// each tool the suppliers list approves, by the name it is installed under.
func deliveredPrograms(t *testing.T, tree *parsed.Tree) map[string]bool {
	t.Helper()
	out := map[string]bool{"go": true, "git": true, "bash": true}
	for _, tool := range tree.Tools(t) {
		name := path.Base(tool.Source)
		if b := tool.Fields["binary"]; b != "" {
			name = b
		}
		out[name] = true
	}
	for _, s := range tree.DeliveriesLock(t) {
		out[s.Name] = true
	}
	return out
}

var (
	// Packages that find the repository's root, or read the repository
	// from one.
	rootFinders = map[string]bool{"homelab/details/repopath": true, "homelab/details/tofufiles": true}
	// Calls that say where a test runs or where it was built.
	whereCalls = map[string]bool{"os.Getwd": true, "os.Chdir": true, "runtime.Caller": true, "runtime.Callers": true, "os.Executable": true}
	// Functions that take a path of the file system.
	fileFunctions = map[string]bool{
		"os.ReadFile": true, "os.Open": true, "os.OpenFile": true, "os.Stat": true, "os.Lstat": true,
		"os.ReadDir": true, "os.DirFS": true, "os.Readlink": true, "filepath.Walk": true,
		"filepath.WalkDir": true, "filepath.Glob": true, "filepath.Abs": true,
		"ioutil.ReadFile": true, "ioutil.ReadDir": true,
	}
	// Programs that read a file as text, or as anything a parser here reads.
	textReaders = map[string]bool{
		"cat": true, "head": true, "tail": true, "less": true, "more": true, "grep": true, "egrep": true,
		"fgrep": true, "rg": true, "sed": true, "awk": true, "cut": true, "tr": true, "sort": true,
		"uniq": true, "wc": true, "xxd": true, "od": true, "hexdump": true, "strings": true,
		"base64": true, "nl": true, "tac": true, "diff": true, "cmp": true, "comm": true, "jq": true,
		"yq": true, "perl": true, "python": true, "python3": true, "ruby": true, "node": true,
	}
	// git's verbs that print what a file holds.
	gitPrinters = map[string]bool{"show": true, "cat-file": true, "grep": true, "blame": true, "archive": true}
)

// textReads is every way one guard file reaches the repository other than
// through tests/parsed, each as "line: what".
func textReads(fset *token.FileSet, f *ast.File, programs map[string]bool, consts map[string]string) []string {
	var out []string
	say := func(n ast.Node, what string) {
		out = append(out, strconv.Itoa(fset.Position(n.Pos()).Line)+": "+what)
	}
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); rootFinders[p] {
			say(imp, "imports "+p+", which finds the repository's root or reads the repository from one")
		}
	}
	name := func(e ast.Expr) (string, bool) {
		if v, ok := stringLiteral(e); ok {
			return v, true
		}
		if id, ok := ast.Unparen(e).(*ast.Ident); ok {
			v, ok := consts[id.Name]
			return v, ok
		}
		return "", false
	}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		called := callName(call)
		sel, _ := call.Fun.(*ast.SelectorExpr)
		switch {
		case whereCalls[called] || sel != nil && sel.Sel.Name == "Chdir":
			say(call, "calls "+called+", which says where the test runs rather than what the repository holds")
		case called == "os.Getenv" || called == "os.LookupEnv":
			if len(call.Args) == 1 {
				v, named := name(call.Args[0])
				arg, _ := call.Args[0].(*ast.SelectorExpr)
				if v == parsed.RootEnv || arg != nil && arg.Sel.Name == "RootEnv" {
					say(call, "reads "+parsed.RootEnv+", the root of the tree being judged, which is tests/parsed's alone")
				} else if !named {
					say(call, "reads an environment variable it does not name, which could be the root of the tree being judged")
				}
			}
		case fileFunctions[called] && len(call.Args) > 0:
			if p, ok := relativePath(call.Args[0]); ok {
				say(call, called+" is given "+strconv.Quote(p)+", a path relative to where the test runs")
			}
			for _, arg := range call.Args {
				if p, ok := climbs(arg); ok {
					say(call, called+" is given "+strconv.Quote(p)+", which climbs")
				}
			}
		}
		at := -1
		switch {
		case called == "exec.Command":
			at = 0
		case called == "exec.CommandContext" || sel != nil && sel.Sel.Name == "Invoke":
			at = 1
		}
		if at < 0 || len(call.Args) <= at {
			return true
		}
		program, named := name(call.Args[at])
		if !named {
			say(call, "starts a program its source does not name; a binary a test built runs through InvokeBinary")
			return true
		}
		if !programs[filepath.Base(program)] {
			say(call, "starts "+program+", which is not go, git, bash or a tool the suppliers list delivers")
			return true
		}
		args := call.Args[at+1:]
		switch filepath.Base(program) {
		case "bash", "sh":
			for i, a := range args {
				if v, _ := stringLiteral(a); v != "-c" || i+1 >= len(args) {
					continue
				}
				script, ok := stringLiteral(args[i+1])
				if !ok {
					continue // the repository's own script, run as shipped
				}
				for _, problem := range scriptReads(script) {
					say(call, problem)
				}
			}
		case "git":
			if verb, ok := gitVerb(args, name); ok && gitPrinters[verb] {
				say(call, "runs git "+verb+", which prints what a file holds")
			}
		}
		return true
	})
	return out
}

// scriptReads is what is wrong with a script a guard writes out for bash: a
// program in it that reads a file as text, or its not being shell at all.
func scriptReads(script string) []string {
	commands, err := parsed.WorkflowCommands("a script a guard wrote", script)
	if err != nil {
		return []string{"hands bash a script that is not shell that can be read: " + err.Error()}
	}
	var out []string
	for _, c := range commands {
		if len(c.Words) > 0 && textReaders[filepath.Base(c.Words[0])] {
			out = append(out, "hands bash a script that runs "+c.Words[0]+", which reads a file as text")
		}
	}
	return out
}

// relativePath is the path a file function is given when it is written out
// relative: a literal, or a Join that starts from one.
func relativePath(e ast.Expr) (string, bool) {
	if v, ok := stringLiteral(e); ok {
		return v, v != "" && !filepath.IsAbs(v)
	}
	if call, ok := ast.Unparen(e).(*ast.CallExpr); ok && callName(call) == "filepath.Join" && len(call.Args) > 0 {
		return relativePath(call.Args[0])
	}
	return "", false
}

// climbs is a literal, anywhere in an argument, that climbs out of where it
// is joined to.
func climbs(e ast.Expr) (string, bool) {
	found, ok := "", false
	ast.Inspect(e, func(n ast.Node) bool {
		if lit, isLit := n.(*ast.BasicLit); isLit && !ok {
			if v, isString := stringLiteral(lit); isString && slices.Contains(strings.Split(filepath.ToSlash(v), "/"), "..") {
				found, ok = v, true
			}
		}
		return !ok
	})
	return found, ok
}

// gitVerb is the verb of a git command whose arguments are written out: the
// first that is not a flag, nor the value of -C or -c.
func gitVerb(args []ast.Expr, name func(ast.Expr) (string, bool)) (string, bool) {
	for i := 0; i < len(args); i++ {
		v, ok := name(args[i])
		if !ok {
			return "", false
		}
		if v == "-C" || v == "-c" {
			i++
			continue
		}
		if !strings.HasPrefix(v, "-") {
			return v, true
		}
	}
	return "", false
}

// Each way a guard could reach the repository other than through
// tests/parsed is refused, and the ways it may run things are not.
func TestTextReadsFindsEveryWayAGuardCouldReadTheRepository(t *testing.T) {
	programs := map[string]bool{"go": true, "git": true, "bash": true, "conftest": true}
	for name, c := range map[string]struct {
		src     string
		refused bool
	}{
		"importing repopath":              {`import "homelab/details/repopath"`, true},
		"importing tofufiles":             {`import "homelab/details/tofufiles"`, true},
		"asking where it runs":            {`func f() { os.Getwd() }`, true},
		"moving where it runs":            {`func f() { os.Chdir("/") }`, true},
		"a test moving where it runs":     {`func f(t *testing.T) { t.Chdir("/") }`, true},
		"asking where its source is":      {`func f() { runtime.Caller(0) }`, true},
		"asking where its binary is":      {`func f() { os.Executable() }`, true},
		"reading the judged root":         {`func f() { os.Getenv(parsed.RootEnv) }`, true},
		"reading it by its name":          {`func f() { os.Getenv("HOMELAB_` + `REPO_ROOT") }`, true},
		"reading a variable unnamed":      {`func f(k string) { os.Getenv(k) }`, true},
		"a relative path":                 {`func f() { os.ReadFile("scripts/retry.sh") }`, true},
		"a relative path joined":          {`func f() { os.ReadDir(filepath.Join("tests", "go")) }`, true},
		"a path that climbs":              {`func f(d string) { os.Stat(filepath.Join(d, "..", "x")) }`, true},
		"a program it does not name":      {`func f(p string) { exec.Command(p) }`, true},
		"a program not delivered":         {`func f(r *parsed.Tree) { r.Invoke(parsed.Run{}, "cat", "x") }`, true},
		"a script that reads text":        {`func f(r *parsed.Tree) { r.Invoke(parsed.Run{}, "bash", "-c", "grep x y") }`, true},
		"git printing a file":             {`func f(r *parsed.Tree) { r.Invoke(parsed.Run{}, "git", "-C", "x", "show", "HEAD:a") }`, true},
		"a delivered tool":                {`const c = "conftest"; func f(r *parsed.Tree) { r.Invoke(parsed.Run{}, c, "test") }`, false},
		"git listing files":               {`func f(r *parsed.Tree) { r.Invoke(parsed.Run{}, "git", "ls-files") }`, false},
		"the repository's own script":     {`func f(r *parsed.Tree, s string) { r.Invoke(parsed.Run{}, "bash", "-c", s) }`, false},
		"a binary a test built":           {`func f(r *parsed.Tree, b string) { r.InvokeBinary(parsed.Run{}, b) }`, false},
		"a file of its own":               {`func f(t *testing.T) { os.ReadFile(filepath.Join(t.TempDir(), "out")) }`, false},
		"a variable it names":             {`func f() { os.Getenv("PATH") }`, false},
		"a fixture that mentions a climb": {`var k = "resources: [../base]"`, false},
	} {
		fixture := parsed.Empty(t)
		fixture.Write(t, "x.go", "package x\n\n"+c.src+"\n")
		fset := token.NewFileSet()
		file := fixture.Go(t, fset, "x.go")
		got := textReads(fset, file, programs, stringConstants(map[string]*ast.File{"x.go": file}))
		if (len(got) > 0) != c.refused {
			t.Errorf("%s: refused=%v, want %v: %v", name, len(got) > 0, c.refused, got)
		}
	}
}
