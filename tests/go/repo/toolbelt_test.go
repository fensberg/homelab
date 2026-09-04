package repo

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The ways to start a subprocess are declared, and there are too many.
//
// Two reasons, and the second is the one that bites.
//
// The estate keeps a small vetted toolbelt on purpose. Eight exported ways to
// do one thing is the same bloat an approved-suppliers list exists to refuse,
// pointed inward: every variant is another shape to maintain, another thing to
// read before writing a caller, and another place a bug can live.
//
// And `scripts/inspector` reads new binary dependencies off a change by
// recognising these calls. Its rule works because every one of them takes the
// executable as a plain string - `name string` immediately before a variadic
// `args ...string`. A NEW shape that does not follow that convention would be
// invisible to it, silently, and the report would still say it found nothing.
// A check that quietly stops covering something is worse than no check.
//
// So the set is written down. Adding one fails here, which is the moment to
// ask whether a ninth variant is needed or whether an existing one would do.
func TestTheWaysToStartASubprocessAreDeclared(t *testing.T) {
	// The current toolbelt. This number should go DOWN.
	declared := map[string]bool{
		"Cmd":            true,
		"CmdBytes":       true,
		"CmdEnv":         true,
		"CmdOutput":      true,
		"CmdOutputBytes": true,
		"CmdOutputEnv":   true,
		"CmdOutputQuiet": true,
		"CmdStdin":       true,

		// Vendor-specific wrappers on top of the generic ones. They are
		// counted here because they are part of the same toolbelt: another
		// caller-facing way to start a subprocess, with its own argument
		// handling to read before using it.
		"Tofu":          true,
		"TofuApply":     true,
		"TofuApplyArgs": true,
	}

	dir := filepath.Join(repoRoot(t), "scripts", "contractor", "internal", "run")
	pkgs, err := parser.ParseDir(token.NewFileSet(), dir, nil, 0)
	if err != nil {
		t.Fatalf("parsing internal/run: %v", err)
	}

	found := map[string]bool{}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			for _, d := range file.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || !fn.Name.IsExported() {
					continue
				}
				if runsASubprocess(fn) {
					found[fn.Name.Name] = true
				}
			}
		}
	}

	if len(found) == 0 {
		t.Fatal("no subprocess helpers found in internal/run, so this test proves nothing")
	}

	var added, gone []string
	for n := range found {
		if !declared[n] {
			added = append(added, n)
		}
	}
	for n := range declared {
		if !found[n] {
			gone = append(gone, n)
		}
	}
	sort.Strings(added)
	sort.Strings(gone)

	for _, n := range added {
		t.Errorf("internal/run.%s is a new way to start a subprocess, and is not declared here.\n\n"+
			"Before declaring it, ask whether one of the %d that already exist would do - this "+
			"number is meant to go down, not up.\n\n"+
			"If it genuinely must exist and it takes an executable, that executable has to be a "+
			"`name string` parameter immediately before `args ...string`. scripts/inspector reads "+
			"new binary dependencies off exactly that shape, so a helper breaking the convention "+
			"becomes invisible to it silently, and the report would still say it found nothing.", n, len(declared))
	}
	for _, n := range gone {
		t.Errorf("internal/run.%s is declared here and no longer exists. Remove the entry - "+
			"the toolbelt getting smaller is the direction this is meant to move.", n)
	}
}

// runsASubprocess reports whether a function's signature is this repository's
// one convention for handing over an executable: a `name string` parameter
// immediately followed by a variadic `args ...string`.
//
// Matched on the shape rather than on the name, so a helper called something
// else entirely is still caught.
func runsASubprocess(fn *ast.FuncDecl) bool {
	var flat []*ast.Field
	for _, p := range fn.Type.Params.List {
		for range max(len(p.Names), 1) {
			flat = append(flat, p)
		}
	}
	if len(flat) < 2 {
		return false
	}

	last := flat[len(flat)-1]
	ell, isVariadic := last.Type.(*ast.Ellipsis)
	if !isVariadic {
		return false
	}
	if id, ok := ell.Elt.(*ast.Ident); !ok || id.Name != "string" {
		return false
	}

	before := flat[len(flat)-2]
	id, ok := before.Type.(*ast.Ident)
	return ok && id.Name == "string"
}
