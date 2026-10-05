package repo

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Every program this repository's Go runs is named by a constant, or is the
// command the operator typed.
//
// WHY THIS EXISTS. Semgrep objects to a command whose arguments are not
// literals, and it is right to ask: a program name that arrives from outside
// is somebody else choosing what runs. Some thirty calls here silence it, and
// until this the reason was the reader's to work out each time.
//
// The reason is always one of two things, and both can be read off the code.
// Go runs a program directly, with no shell between, so an argument cannot
// become a command: what matters is the program's name, and whether the
// program is itself an interpreter handed a script. So:
//
//   - the name is a string constant where the program is run; or
//   - it is a parameter, and every caller of that function passes a constant,
//     or its own parameter, and so on up; or
//   - it is the operator's: read from the process's own command line, which
//     is the person at the keyboard choosing what to run, as with
//     `contractor kubeconfig -- <command>`.
//
// And a shell or an interpreter is handed its program as a constant: the
// script, the module, or what follows -c. For those an argument is a program,
// and the name of the interpreter says nothing about what runs.
//
// A test is held to less, and only where that is what a test is for. It runs
// the repository's own scripts and the programs it has just built, so in a
// test a program may also be a path the test put together, and what a shell
// is handed is not looked at.
//
// Anything this cannot follow fails. It reads the syntax and not the types, so
// what it accepts is narrow on purpose: a name built at run time, read from a
// file or taken from a struct is refused, whether or not it happens to be safe.

// interpreters take a program as an argument.
var interpreters = map[string]bool{
	"sh": true, "bash": true, "dash": true, "zsh": true, "ksh": true,
	"python": true, "python3": true, "node": true, "perl": true, "ruby": true,
	"pwsh": true, "powershell": true, "cmd": true, "env": true, "xargs": true,
}

// goFunc is one function declaration and where it is.
type goFunc struct {
	dir  string
	file string
	decl *ast.FuncDecl
}

func (f *goFunc) isTest() bool { return strings.HasSuffix(f.file, "_test.go") }

// calledAs says whether a call's function expression names f: by its bare
// name from its own package, by package and name from another, or as a method.
func (f *goFunc) calledAs(fun ast.Expr, from *goFunc) bool {
	name := f.decl.Name.Name
	switch fun := fun.(type) {
	case *ast.Ident:
		return fun.Name == name && from.dir == f.dir && f.decl.Recv == nil
	case *ast.SelectorExpr:
		if fun.Sel.Name != name || !ast.IsExported(name) && f.decl.Recv == nil {
			return false
		}
		if f.decl.Recv != nil {
			return true
		}
		x, ok := fun.X.(*ast.Ident)
		return ok && x.Name == filepath.Base(f.dir)
	}
	return false
}

func (f *goFunc) param(name string) int {
	i := 0
	for _, field := range f.decl.Type.Params.List {
		for _, n := range field.Names {
			if n.Name == name {
				return i
			}
			i++
		}
		if len(field.Names) == 0 {
			i++
		}
	}
	return -1
}

// goSource is every tracked Go file, parsed.
type goSource struct {
	fset   *token.FileSet
	funcs  []*goFunc
	byName map[string][]*goFunc
	// consts is the names of string constants, by the directory declaring
	// them and, for one named from another package, by name alone.
	consts    map[string]map[string]bool
	anyConst  map[string]bool
	files     map[string]*ast.File
	checked   map[string]string
	exploring map[string]bool
}

func readGoSource(t *testing.T) *goSource {
	t.Helper()
	s := &goSource{
		fset: token.NewFileSet(), byName: map[string][]*goFunc{}, consts: map[string]map[string]bool{},
		anyConst: map[string]bool{}, files: map[string]*ast.File{}, checked: map[string]string{}, exploring: map[string]bool{},
	}
	for _, rel := range goFiles(t) {
		file, err := parser.ParseFile(s.fset, filepath.Join(repoRoot(t), rel), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", rel, err)
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		s.files[rel] = file
		if s.consts[dir] == nil {
			s.consts[dir] = map[string]bool{}
		}
		for _, d := range file.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				f := &goFunc{dir: dir, file: rel, decl: d}
				s.funcs = append(s.funcs, f)
				s.byName[d.Name.Name] = append(s.byName[d.Name.Name], f)
			case *ast.GenDecl:
				if d.Tok != token.CONST {
					continue
				}
				for _, spec := range d.Specs {
					v := spec.(*ast.ValueSpec)
					for i, n := range v.Names {
						if i < len(v.Values) && isStringLiteral(v.Values[i]) {
							s.consts[dir][n.Name] = true
							s.anyConst[n.Name] = true
						}
					}
				}
			}
		}
	}
	if len(s.funcs) == 0 {
		t.Fatal("parsed no Go functions, so no program that is run was looked at")
	}
	return s
}

// isConstant is a string literal, or a string constant f's package declares.
func (s *goSource) isConstant(e ast.Expr, f *goFunc) bool {
	if id, ok := e.(*ast.Ident); ok {
		return s.consts[f.dir][id.Name]
	}
	return isStringLiteral(e)
}

func isStringLiteral(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING
}

func (s *goSource) at(f *goFunc, n ast.Node) string {
	return f.file + ":" + strconv.Itoa(s.fset.Position(n.Pos()).Line)
}

// assignments is every expression assigned to a local of this name in f.
func assignments(f *goFunc, name string) []ast.Expr {
	var out []ast.Expr
	if f.decl.Body == nil {
		return nil
	}
	ast.Inspect(f.decl.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range n.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name == name {
					if len(n.Rhs) == len(n.Lhs) {
						out = append(out, n.Rhs[i])
					} else if len(n.Rhs) == 1 {
						out = append(out, n.Rhs[0])
					}
				}
			}
		case *ast.ValueSpec:
			for i, id := range n.Names {
				if id.Name == name && i < len(n.Values) {
					out = append(out, n.Values[i])
				}
			}
		}
		return true
	})
	return out
}

// named says why the expression e, in f, is not a program name this can
// vouch for, or "" when it is a constant or the operator's.
func (s *goSource) named(e ast.Expr, f *goFunc) string {
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			return ""
		}
	case *ast.ParenExpr:
		return s.named(e.X, f)
	case *ast.Ident:
		if e.Name == "nil" || s.consts[f.dir][e.Name] {
			return ""
		}
		if i := f.param(e.Name); i >= 0 {
			return s.callersPass(f, i)
		}
		rhs := assignments(f, e.Name)
		if len(rhs) == 0 {
			return s.at(f, e) + ": " + e.Name + " is not a constant, a parameter or a local this can follow"
		}
		for _, r := range rhs {
			if why := s.named(r, f); why != "" {
				return why
			}
		}
		return ""
	case *ast.SelectorExpr:
		if x, ok := e.X.(*ast.Ident); ok {
			if x.Name == "os" && e.Sel.Name == "Args" {
				return ""
			}
			// A constant another package declares.
			if s.anyConst[e.Sel.Name] && f.param(x.Name) < 0 && len(assignments(f, x.Name)) == 0 {
				return ""
			}
		}
	case *ast.CompositeLit:
		// A command written out where it is passed: its first word is the
		// program.
		if len(e.Elts) > 0 {
			return s.named(e.Elts[0], f)
		}
	case *ast.IndexExpr:
		return s.named(e.X, f)
	case *ast.SliceExpr:
		return s.named(e.X, f)
	case *ast.StarExpr:
		// A flag: what the operator passed, or the constant it defaults to.
		if id, ok := e.X.(*ast.Ident); ok {
			for _, r := range assignments(f, id.Name) {
				call, ok := r.(*ast.CallExpr)
				if !ok {
					return s.at(f, e) + ": *" + id.Name + " is not a flag's value"
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "String" || len(call.Args) < 2 || !s.isConstant(call.Args[1], f) {
					return s.at(f, e) + ": *" + id.Name + " is not a string flag with a constant default"
				}
			}
			if len(assignments(f, id.Name)) > 0 {
				return ""
			}
		}
	case *ast.CallExpr:
		// In a test, a path the test built: a script of the repository's, or
		// a program compiled a moment ago.
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok && f.isTest() {
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "filepath" && sel.Sel.Name == "Join" {
				return ""
			}
		}
		// A function that hands back the operator's command line and nothing else.
		name := ""
		switch fun := e.Fun.(type) {
		case *ast.Ident:
			name = fun.Name
		case *ast.SelectorExpr:
			name = fun.Sel.Name
		}
		for _, callee := range s.byName[name] {
			if callee.decl.Body == nil {
				continue
			}
			why, returns := "", 0
			ast.Inspect(callee.decl.Body, func(n ast.Node) bool {
				ret, ok := n.(*ast.ReturnStmt)
				if !ok || why != "" {
					return true
				}
				for _, r := range ret.Results {
					returns++
					if w := s.named(r, callee); w != "" {
						why = w
					}
				}
				return true
			})
			if why != "" {
				return why
			}
			if returns > 0 {
				return ""
			}
		}
	}
	return s.at(f, e) + ": the program's name is worked out at run time"
}

// callersPass says why some caller of f does not pass a name this can vouch
// for as its i'th argument, or "".
func (s *goSource) callersPass(f *goFunc, i int) string {
	key := f.dir + "." + f.decl.Name.Name + "#" + strconv.Itoa(i)
	if why, done := s.checked[key]; done {
		return why
	}
	if s.exploring[key] {
		return ""
	}
	s.exploring[key] = true
	why := ""
	for _, caller := range s.funcs {
		if caller.decl.Body == nil || why != "" {
			continue
		}
		ast.Inspect(caller.decl.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || why != "" {
				return true
			}
			if !f.calledAs(call.Fun, caller) {
				return true
			}
			if i >= len(call.Args) {
				why = s.at(caller, call) + ": calls " + f.decl.Name.Name + " without the argument that names the program"
				return true
			}
			if why = s.named(call.Args[i], caller); why == "" {
				why = s.interpreterGivenAScript(call.Args[i], call.Args[i+1:], call.Ellipsis.IsValid(), caller)
			}
			return true
		})
	}
	delete(s.exploring, key)
	s.checked[key] = why
	return why
}

// takesAProgram is the flags after which an interpreter's next argument is
// the program it runs.
var takesAProgram = map[string]bool{"-c": true, "-e": true, "--command": true, "--eval": true, "-m": true}

// leadingConstants is the constant strings a list of arguments starts with,
// following a slice that is spread into the call back to where it was made.
func (s *goSource) leadingConstants(args []ast.Expr, spread bool, f *goFunc) []string {
	var out []string
	for i, a := range args {
		if spread && i == len(args)-1 {
			return append(out, s.leadingOfSlice(a, f)...)
		}
		lit, ok := a.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return out
		}
		v, err := strconv.Unquote(lit.Value)
		if err != nil {
			return out
		}
		out = append(out, v)
	}
	return out
}

func (s *goSource) leadingOfSlice(e ast.Expr, f *goFunc) []string {
	switch e := e.(type) {
	case *ast.CompositeLit:
		return s.leadingConstants(e.Elts, false, f)
	case *ast.CallExpr:
		if id, ok := e.Fun.(*ast.Ident); ok && id.Name == "append" && len(e.Args) > 0 {
			return s.leadingOfSlice(e.Args[0], f)
		}
	case *ast.Ident:
		// Where the slice was first made; what is appended later comes after.
		if rhs := assignments(f, e.Name); len(rhs) > 0 {
			if _, self := rhs[0].(*ast.Ident); !self {
				return s.leadingOfSlice(rhs[0], f)
			}
		}
	}
	return nil
}

// interpreterGivenAScript refuses a shell or an interpreter that is not
// handed its program as a constant.
func (s *goSource) interpreterGivenAScript(program ast.Expr, args []ast.Expr, spread bool, f *goFunc) string {
	lit, ok := program.(*ast.BasicLit)
	if !ok || f.isTest() {
		return ""
	}
	name, err := strconv.Unquote(lit.Value)
	if err != nil || !interpreters[filepath.Base(name)] {
		return ""
	}
	leading := s.leadingConstants(args, spread, f)
	for i := 0; i < len(leading); i++ {
		switch {
		case takesAProgram[leading[i]]:
			if i+1 < len(leading) {
				return ""
			}
			return s.at(f, program) + ": " + name + " " + leading[i] + " is handed its program at run time"
		case strings.HasPrefix(leading[i], "-"):
		default:
			return ""
		}
	}
	return s.at(f, program) + ": " + name + " is handed what it runs at run time, and for an interpreter an argument is a program"
}

func TestEveryProgramRunIsNamedByAConstantOrIsTheOperators(t *testing.T) {
	s := readGoSource(t)

	var failures []string
	runs := 0
	for _, f := range s.funcs {
		if f.decl.Body == nil {
			continue
		}
		ast.Inspect(f.decl.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok || x.Name != "exec" {
				return true
			}
			at := 0
			switch sel.Sel.Name {
			case "Command":
			case "CommandContext":
				at = 1
			default:
				return true
			}
			runs++
			if at >= len(call.Args) {
				failures = append(failures, s.at(f, call)+": runs a program without naming one")
				return true
			}
			why := s.named(call.Args[at], f)
			if why == "" {
				why = s.interpreterGivenAScript(call.Args[at], call.Args[at+1:], call.Ellipsis.IsValid(), f)
			}
			if why != "" {
				failures = append(failures, s.at(f, call)+" runs a program this cannot vouch for\n      "+why)
			}
			return true
		})
	}
	if runs < 20 {
		t.Fatalf("found %d place(s) where a program is run, which is too few to be this repository's: the search has stopped matching", runs)
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		t.Errorf("%d of %d place(s) run a program whose name is not a constant and not the operator's:\n\n  %s\n\n"+
			"Name the program with a constant where it is run, or pass it down as a parameter from "+
			"callers that do. A name read from a file, a struct or the network is somebody else "+
			"choosing what this runs.", len(failures), runs, strings.Join(failures, "\n  "))
	}
}
