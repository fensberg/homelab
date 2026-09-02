package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Nothing in a sequence may reach OpenTofu before `attach` does.
//
// `attach` is the phase that points this workspace at the state living in the
// cluster, and it refuses to run when a local `terraform.tfstate` exists -
// correctly, because local state means an ignition that never reached Migrate,
// and attaching on top of it would leave two states describing one estate.
//
// The trap is that any earlier tofu command creates exactly that condition.
// `tofu init` in the cluster directory configures the local backend, an apply
// writes `terraform.tfstate` beside it, and `attach` then reports the workspace
// as mid-ignition. The sequence refuses itself.
//
// It happened. ConvergePhases ran `overlay` second, and the first converge ever
// to get past Verify halted at Attach on a state file its own third phase had
// written thirty seconds earlier. Nothing caught it because each phase was
// correct alone and the ordering was only wrong in combination - and because
// the converge lane had never reached Attach before, having failed at Verify
// every previous time for an unrelated reason.
func TestNothingRunsTofuBeforeAttach(t *testing.T) {
	for name, seq := range sequencesWithAttach(t) {
		attachAt := indexOf(seq, "attach")
		for i, phase := range seq[:attachAt] {
			if body := phaseSource(t, phase); mentionsTofu(body) {
				t.Errorf("%s runs %q at position %d, before attach at position %d, and %s.go invokes tofu.\n\n"+
					"tofu in the cluster directory configures the local backend and writes "+
					"terraform.tfstate, which is the condition attach refuses. Move it after "+
					"attach, or drop it from this sequence.", name, phase, i, attachAt, phase)
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
	dir := filepath.Join(repoRoot(t), "scripts", "contractor", "internal", "phases")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		body := string(raw)
		for _, at := range streamingTofuCall.FindAllStringIndex(body, -1) {
			call := body[at[0]:min(at[0]+300, len(body))]
			sub, found := firstSubcommand(call)
			switch {
			case !found:
				t.Errorf("%s streams tofu with no literal subcommand in the call:\n\n  %s\n\n"+
					"This check cannot tell what will run, and an argument list built "+
					"elsewhere is exactly how `tofu apply` slipped past the version of "+
					"this test that searched for the word. Pass the subcommand here, or "+
					"use run.TofuApply / run.TofuApplyArgs / run.TofuDestroy.",
					e.Name(), strings.TrimSpace(firstLine(call)))
			case !quietSubcommands[sub]:
				t.Errorf("%s streams `tofu %s`, which prints resource attributes:\n\n  %s\n\n"+
					"A converge runs in a public Actions log and a destroy prints the "+
					"estate's own certificate authorities. Use run.TofuApply, "+
					"run.TofuApplyArgs or run.TofuDestroy, which report an address and a "+
					"verb and no values.", e.Name(), sub, strings.TrimSpace(firstLine(call)))
			}
		}
	}
}

// Subcommands that cannot print a resource attribute. Everything absent from
// this list is refused rather than allowed, so a subcommand nobody considered
// fails closed.
var quietSubcommands = map[string]bool{
	"init": true, "state": true, "validate": true,
	"fmt": true, "version": true, "providers": true, "workspace": true,
}

// firstSubcommand returns the first quoted argument in a call that looks like
// a tofu subcommand - a bare lower-case word, not a flag and not a label.
func firstSubcommand(call string) (string, bool) {
	for _, m := range quotedArg.FindAllStringSubmatch(call, -1) {
		arg := m[1]
		if strings.HasPrefix(arg, "-") || strings.ContainsAny(arg, " ./=") {
			continue
		}
		if arg == "tofu" {
			continue // the command name, not its subcommand
		}
		return arg, true
	}
	return "", false
}

var quotedArg = regexp.MustCompile(`"([^"]*)"`)

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
	dir := filepath.Join(repoRoot(t), "scripts", "contractor", "internal", "phases")
	for name, seq := range sequencesWithAttach(t) {
		for _, phase := range seq {
			if _, err := os.Stat(filepath.Join(dir, phase+".go")); err != nil {
				t.Errorf("%s names the phase %q but there is no %s.go. The ordering "+
					"checks find a phase's code by that name, so this phase is not being "+
					"checked at all.", name, phase, phase)
			}
		}
	}
}

// Every way of reaching tofu that streams its output: the thin wrapper, and
// the general command runner called with tofu directly. Deliberately not
// run.TofuApply / TofuApplyArgs / TofuDestroy, which summarise, nor the
// CmdOutput family, which capture.
var streamingTofuCall = regexp.MustCompile(`run\.Tofu\(|run\.Cmd\((?:[^,]*,\s*)?"tofu"`)

// Anything that shells out to tofu. Deliberately broad: the question is not
// which subcommand runs, it is whether the cluster directory acquires a local
// backend before attach expects it to be clean.
var tofuCall = regexp.MustCompile(`run\.Tofu[A-Za-z]*\(|"tofu"`)

func mentionsTofu(body string) bool { return tofuCall.MatchString(body) }

func phaseSource(t *testing.T, phase string) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), "scripts", "contractor", "internal", "phases", phase+".go")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(body)
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
	path := filepath.Join(repoRoot(t), "scripts", "contractor", "internal", "phases", "registry.go")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	found := map[string][]string{}
	for _, m := range sequenceDecl.FindAllStringSubmatch(string(body), -1) {
		seq := phaseName.FindAllString(m[2], -1)
		for i := range seq {
			seq[i] = strings.Trim(seq[i], `"`)
		}
		if indexOf(seq, "attach") >= 0 {
			found[m[1]] = seq
		}
	}
	if len(found) == 0 {
		t.Fatal("no sequence in registry.go contains an attach phase. Either the phase " +
			"was renamed or this test can no longer find the declarations it checks; " +
			"passing on nothing is not the same as passing.")
	}
	return found
}

var sequenceDecl = regexp.MustCompile(`(?s)var (\w+Phases) = \[\]string\{(.*?)\}`)
var phaseName = regexp.MustCompile(`"[a-z-]+"`)
