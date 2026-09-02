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

// `run.Tofu` may only run `init`.
//
// It streams whatever tofu prints, straight through. For `init` that is
// provider downloads and backend configuration - no resource attributes, so
// nothing that came out of the vault. For anything else it is the planned
// change in full, every non-sensitive attribute of everything touched.
//
// The overlay phase applied through it and published
// `description = "<site> subnet router"` - the site's name, a vault value -
// into a public repository's Actions log. Values had been guarded for a while
// by then; this was the third distinct path by which a name reached that log
// without a single attribute being printed on purpose.
//
// `run.TofuApply` and `run.TofuApplyArgs` read tofu's -json stream instead and
// report an address and a verb per resource. The rule is an allowlist rather
// than a search for the word "apply" because the argument list is often built
// elsewhere: the first version of this check looked for a literal "apply" in
// the call, and the very call that caused the leak passed `args...` and slipped
// straight through it.
func TestTheStreamingTofuHelperOnlyEverRunsInit(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "scripts", "contractor", "internal", "phases")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		for _, at := range streamingTofuCall.FindAllStringIndex(string(body), -1) {
			call := string(body)[at[0]:min(at[0]+240, len(body))]
			if strings.Contains(call, `"init"`) {
				continue
			}
			t.Errorf("%s calls run.Tofu with something other than init:\n\n  %s\n\n"+
				"run.Tofu streams what tofu prints, and anything but init prints resource "+
				"attributes - a converge runs in a public Actions log. Use run.TofuApply "+
				"or run.TofuApplyArgs, which report an address and a verb and no values.",
				e.Name(), strings.TrimSpace(firstLine(call)))
		}
	}
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

// The streaming helper, exactly - not TofuApply, TofuInit or TofuOutputRaw,
// whose names all begin the same way.
var streamingTofuCall = regexp.MustCompile(`run\.Tofu\(`)

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
