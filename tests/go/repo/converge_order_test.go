package repo

import (
	"go/ast"
	"go/printer"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Nothing in a sequence may reach OpenTofu before `take-over` does.
//
// `take-over` is the phase that points this workspace at the state living in the
// cluster, and it refuses to run when a local `terraform.tfstate` exists -
// correctly, because local state means an ignition that never reached Migrate,
// and taking over on top of it would leave two states describing one estate.
//
// The trap is that any earlier tofu command creates exactly that condition.
// `tofu init` in the cluster directory configures the local backend, an apply
// writes `terraform.tfstate` beside it, and `take-over` then reports the workspace
// as mid-ignition. The sequence refuses itself.
//
// It happened. ConvergePhases ran `overlay` second, and the first converge ever
// to get past Verify halted at TakeOver on a state file its own third phase had
// written thirty seconds earlier. Nothing caught it because each phase was
// correct alone and the ordering was only wrong in combination - and because
// the converge lane had never reached TakeOver before, having failed at Verify
// every previous time for an unrelated reason.
func TestNothingRunsTofuBeforeAttach(t *testing.T) {
	light(t)
	for name, seq := range sequencesWithAttach(t) {
		takeOverAt := indexOf(seq, "take-over")
		for i, phase := range seq[:takeOverAt] {
			if mentionsTofu(phaseSource(t, phase)) {
				t.Errorf("%s runs %q at position %d, before take-over at position %d, and %s.go invokes tofu.\n\n"+
					"tofu in the cluster directory configures the local backend and writes "+
					"terraform.tfstate, which is the condition take-over refuses. Move it after "+
					"take-over, or drop it from this sequence.", name, phase, i, takeOverAt, phase)
			}
		}
	}
}

// A tofu subcommand that can print resource attributes must not be run
// through a helper that streams.
//
// Two helpers stream what the command prints: `run.Tofu`, and `run.Cmd` called
// with "tofu" directly. For `init` that is provider downloads and backend
// configuration, and for `state`/`validate`/`fmt` it is addresses or nothing -
// none of which came out of the vault. For `apply`, `destroy`, `plan`, `show`
// and `refresh` it is the resources themselves, every attribute the provider
// did not mark sensitive.
//
// The first version of this check only knew about `run.Tofu`, and asserted it
// could only run `init`. That passed while `tofu destroy` sat one function
// away going through `run.Cmd` - printing `talos_machine_secrets` in full, the
// etcd, Kubernetes, aggregator and OS certificate authorities and the cluster
// id, none marked sensitive by the provider. A guard that names one helper
// tests the helper; what needed guarding was the subcommand.
//
// So the allowlist is of subcommands, and it applies to every way of reaching
// tofu. `run.TofuApply`, `run.TofuApplyArgs` and `run.TofuDestroy` are the
// ways to run the rest: they read tofu's -json stream and report an address
// and a verb per resource.
func TestOnlyQuietTofuSubcommandsAreStreamed(t *testing.T) {
	light(t)
	tree := repository(t)
	checked := 0
	for _, rel := range tree.Files(t) {
		if filepath.Dir(rel) != phasesDir || !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			continue
		}
		checked++
		name := filepath.Base(rel)
		fset := token.NewFileSet()
		ast.Inspect(tree.Go(t, fset, rel), func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !streamsTofu(call) {
				return true
			}
			var printed strings.Builder
			_ = printer.Fprint(&printed, fset, call)
			shown := firstLine(printed.String())
			sub, found := firstSubcommand(call)
			switch {
			case !found:
				t.Errorf("%s streams tofu with no literal subcommand in the call:\n\n  %s\n\n"+
					"This check cannot tell what will run, and an argument list built "+
					"elsewhere is exactly how `tofu apply` slipped past the version of "+
					"this test that searched for the word. Pass the subcommand here, or "+
					"use run.TofuApply / run.TofuApplyArgs / run.TofuDestroy.",
					name, shown)
			case !quietSubcommands[sub]:
				t.Errorf("%s streams `tofu %s`, which prints resource attributes:\n\n  %s\n\n"+
					"A converge runs in a public Actions log and a destroy prints the "+
					"estate's own certificate authorities. Use run.TofuApply, "+
					"run.TofuApplyArgs or run.TofuDestroy, which report an address and a "+
					"verb and no values.", name, sub, shown)
			}
			return true
		})
	}
	// The phases directory is this guard's whole subject, and reading zero
	// files from it passes: the loop below asserts something about each file,
	// so no files means no assertions and a green check. That is reachable by
	// ordinary maintenance - the phases moving one level down would have every
	// entry skipped as a directory - and it announces itself nowhere.
	//
	// A real minimum rather than "at least one", because the interesting
	// failure is a filter that still matches something. There were 24 at the
	// time of writing; this is deliberately well below that, so it catches a
	// collapse without failing on ordinary consolidation.
	if checked < 10 {
		t.Fatalf("only %d phase source files were checked, which cannot be right - this guard is reading the wrong directory and proves nothing", checked)
	}
}

// Subcommands that cannot print a resource attribute. Everything absent from
// this list is refused rather than allowed, so a subcommand nobody considered
// fails closed.
var quietSubcommands = map[string]bool{
	"init": true, "state": true, "validate": true,
	"fmt": true, "version": true, "providers": true, "workspace": true,
}

// firstSubcommand returns the first string literal among a call's arguments
// that looks like a tofu subcommand - a bare lower-case word, not a flag and
// not a label.
func firstSubcommand(call *ast.CallExpr) (string, bool) {
	sub, found := "", false
	for _, arg := range call.Args {
		ast.Inspect(arg, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if found || !ok || lit.Kind != token.STRING {
				return !found
			}
			v, err := strconv.Unquote(lit.Value)
			if err != nil || v == "tofu" || strings.HasPrefix(v, "-") || strings.ContainsAny(v, " ./=") {
				return true
			}
			sub, found = v, true
			return false
		})
		if found {
			break
		}
	}
	return sub, found
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Every phase a sequence names must have a source file called after it. The
// two tests above locate a phase's code that way, so a phase that broke the
// convention would be silently exempt from both.
func TestEveryPhaseHasASourceFileNamedAfterIt(t *testing.T) {
	light(t)
	for name, seq := range sequencesWithAttach(t) {
		for _, phase := range seq {
			if !repository(t).Exists(phasesDir + "/" + phase + ".go") {
				t.Errorf("%s names the phase %q but there is no %s.go. The ordering "+
					"checks find a phase's code by that name, so this phase is not being "+
					"checked at all.", name, phase, phase)
			}
		}
	}
}

// Where the converge's phases are.
const phasesDir = "scripts/contractor/internal/phases"

// streamsTofu reports whether a call is one of the ways of reaching tofu that
// stream its output: the thin wrapper, and the general command runner called
// with tofu directly, as its first or second argument. Deliberately not
// run.TofuApply / TofuApplyArgs / TofuDestroy, which summarise, nor the
// CmdOutput family, which capture.
func streamsTofu(call *ast.CallExpr) bool {
	switch callName(call) {
	case "run.Tofu":
		return true
	case "run.Cmd":
		for i, arg := range call.Args {
			if lit, ok := arg.(*ast.BasicLit); ok && i < 2 && lit.Kind == token.STRING && lit.Value == `"tofu"` {
				return true
			}
		}
	}
	return false
}

// mentionsTofu reports whether a file shells out to tofu at all: any run.Tofu
// helper, or the name as a string. Deliberately broad: the question is not
// which subcommand runs, it is whether the cluster directory acquires a local
// backend before take-over expects it to be clean.
func mentionsTofu(f *ast.File) bool {
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			found = found || strings.HasPrefix(callName(n), "run.Tofu")
		case *ast.BasicLit:
			found = found || n.Kind == token.STRING && n.Value == `"tofu"`
		}
		return !found
	})
	return found
}

func phaseSource(t *testing.T, phase string) *ast.File {
	t.Helper()
	return repository(t).Go(t, token.NewFileSet(), phasesDir+"/"+phase+".go")
}

func indexOf(seq []string, want string) int {
	for i, s := range seq {
		if s == want {
			return i
		}
	}
	return -1
}

// The sequences are read out of registry.go rather than imported, because
// tests/go is its own module and scripts/contractor is another. Reading the
// declaration keeps one source of truth: a sequence renamed or added here
// without being declared there fails loudly rather than quietly checking
// nothing.
func sequencesWithAttach(t *testing.T) map[string][]string {
	t.Helper()
	found := map[string][]string{}
	for _, d := range repository(t).Go(t, token.NewFileSet(), phasesDir+"/registry.go").Decls {
		gen, ok := d.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 || !strings.HasSuffix(vs.Names[0].Name, "Phases") {
				continue
			}
			lit, ok := vs.Values[0].(*ast.CompositeLit)
			if !ok {
				continue
			}
			var seq []string
			for _, e := range lit.Elts {
				if b, ok := e.(*ast.BasicLit); ok && b.Kind == token.STRING {
					if v, err := strconv.Unquote(b.Value); err == nil {
						seq = append(seq, v)
					}
				}
			}
			if indexOf(seq, "take-over") >= 0 {
				found[vs.Names[0].Name] = seq
			}
		}
	}
	if len(found) == 0 {
		t.Fatal("no sequence in registry.go contains a take-over phase. Either the phase " +
			"was renamed or this test can no longer find the declarations it checks; " +
			"passing on nothing is not the same as passing.")
	}
	return found
}
