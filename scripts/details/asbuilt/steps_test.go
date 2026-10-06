package asbuilt

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type stepTofu struct {
	calls  [][]string
	state  string
	failOn string
}

func (f *stepTofu) run(_ string, _ []string, args ...string) ([]byte, []byte, error) {
	f.calls = append(f.calls, args)
	for _, a := range args {
		if f.failOn != "" && a == "-target="+f.failOn {
			return nil, []byte("Error: Moved resource instances excluded by targeting"), errors.New("exit 1")
		}
	}
	switch {
	case args[0] == "show":
		return []byte(NothingPlanned), nil, nil
	case args[0] == "state" && args[1] == "list":
		return []byte(f.state), nil, nil
	}
	return nil, nil, nil
}

func (f *stepTofu) plans() [][]string {
	var out [][]string
	for _, c := range f.calls {
		if c[0] == "plan" {
			out = append(out, c)
		}
	}
	return out
}

var sequence = []PlanStep{
	{Label: "vms", Targets: []string{"vm.cp"}},
	{Label: "config", Targets: []string{"cfg.cp", "cfg.worker"}},
	{Label: "everything"},
}

// Each step is planned with its own targets, in the converge's order, and the
// plan returned is the last one's - the untargeted whole.
func TestTheStepsArePlannedInOrderWithTheirOwnTargets(t *testing.T) {
	f := &stepTofu{}
	plan, err := PlanSteps(StepsInputs{Dir: t.TempDir(), Sequence: sequence}, f.run)
	if err != nil {
		t.Fatal(err)
	}
	plans := f.plans()
	if len(plans) != 3 {
		t.Fatalf("planned %d times, want 3", len(plans))
	}
	targets := func(args []string) []string {
		var out []string
		for _, a := range args {
			if strings.HasPrefix(a, "-target=") {
				out = append(out, strings.TrimPrefix(a, "-target="))
			}
		}
		return out
	}
	if !slices.Equal(targets(plans[0]), []string{"vm.cp"}) || !slices.Equal(targets(plans[1]), []string{"cfg.cp", "cfg.worker"}) || len(targets(plans[2])) != 0 {
		t.Errorf("the steps were planned as %v", plans)
	}
	for _, p := range plans {
		if !slices.Contains(p, "-refresh=false") {
			t.Errorf("a plan against the record refreshed: %v", p)
		}
	}
	if !strings.Contains(string(plan), "resource_changes") {
		t.Errorf("the whole plan was not returned: %s", plan)
	}

	f = &stepTofu{}
	if _, err := PlanSteps(StepsInputs{Dir: t.TempDir(), Sequence: sequence, Refresh: true}, f.run); err != nil {
		t.Fatal(err)
	}
	for _, p := range f.plans() {
		if slices.Contains(p, "-refresh=false") {
			t.Errorf("a plan against the estate did not refresh: %v", p)
		}
	}
}

// A sequence that does not end with the untargeted apply would plan less than
// the converge does.
func TestASequenceMustEndUntargeted(t *testing.T) {
	for _, seq := range [][]PlanStep{nil, sequence[:2]} {
		if _, err := PlanSteps(StepsInputs{Dir: t.TempDir(), Sequence: seq}, (&stepTofu{}).run); err == nil {
			t.Errorf("the sequence %v was accepted", seq)
		}
	}
}

// A step the converge would have refused is refused here, by name - the
// pull request that halted every converge on main for a day would have
// failed its plan instead.
func TestARefusedStepIsNamed(t *testing.T) {
	f := &stepTofu{failOn: "cfg.worker"}
	_, err := PlanSteps(StepsInputs{Dir: t.TempDir(), Sequence: sequence}, f.run)
	if err == nil || !strings.Contains(err.Error(), `"config"`) || !strings.Contains(err.Error(), "excluded by targeting") {
		t.Errorf("got %v", err)
	}
}

// Against the record a rename is recorded with state mv, only when the old
// address is in state and the new one is not; against the estate it is the
// converge's own refresh-only apply at both ends.
func TestRenamesAreSettledInTheCopyFirst(t *testing.T) {
	dir := t.TempDir()
	body := "moved {\n  from = vm.old\n  to   = vm.cp\n}\nmoved {\n  from = gone.one\n  to   = gone.two\n}\nmoved {\n  from = both.a\n  to   = both.b\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "moves.tf"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &stepTofu{state: "vm.old[\"n\"]\nboth.a\nboth.b\nother.x\n"}
	if _, err := PlanSteps(StepsInputs{Dir: dir, Sequence: sequence}, f.run); err != nil {
		t.Fatal(err)
	}
	var mvs [][]string
	for _, c := range f.calls {
		if c[0] == "state" && c[1] == "mv" {
			mvs = append(mvs, c)
		}
	}
	if len(mvs) != 1 || !slices.Equal(mvs[0][len(mvs[0])-2:], []string{"vm.old", "vm.cp"}) {
		t.Errorf("recorded renames %v, want only vm.old -> vm.cp", mvs)
	}
	firstPlan, lastMv := -1, -1
	for i, c := range f.calls {
		if c[0] == "plan" && firstPlan < 0 {
			firstPlan = i
		}
		if c[0] == "state" && c[1] == "mv" {
			lastMv = i
		}
	}
	if lastMv > firstPlan {
		t.Errorf("a rename was recorded at call %d, after the first plan at %d", lastMv, firstPlan)
	}

	f = &stepTofu{}
	if _, err := PlanSteps(StepsInputs{Dir: dir, Sequence: sequence, Refresh: true}, f.run); err != nil {
		t.Fatal(err)
	}
	var settle []string
	for _, c := range f.calls {
		if c[0] == "apply" {
			settle = c
			break
		}
		if c[0] == "plan" {
			break
		}
	}
	if settle == nil || !slices.Contains(settle, "-refresh-only") || !slices.Contains(settle, "-target=vm.old") || !slices.Contains(settle, "-target=vm.cp") {
		t.Errorf("against the estate, renames were not settled by a refresh-only apply before the first plan: %v", f.calls)
	}
}

func TestInStateMatchesInstances(t *testing.T) {
	addrs := []string{`a.b["k"]`, "c.d", "c.de"}
	if !inState(addrs, "a.b") || !inState(addrs, "c.d") || inState(addrs, "c") || inState(addrs, "a.bb") {
		t.Error("inState matched the wrong resources")
	}
}

// The exported wrappers do what the package's own helpers do.
func TestCopyRootAndPluginDirAreTheHelpers(t *testing.T) {
	root := t.TempDir()
	from := filepath.Join(root, "management", "cluster")
	if err := os.MkdirAll(from, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(from, "main.tf"), []byte("# x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(root, ".as-built", "plan")
	if err := CopyRoot(from, to); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(to, "main.tf")); err != nil {
		t.Errorf("the root was not copied: %v", err)
	}
	if got := PluginDir("/r"); got != "-plugin-dir=/r/.terraform/providers" {
		t.Errorf("got %q", got)
	}
}

// Every cached data-source read is forgotten, and nothing else. A read left
// at an address the configuration no longer uses is an implicit move, which
// refuses every targeted apply - how #555's count on the health read would
// have halted the next converge.
func TestCachedReadsAreForgottenAndNothingElse(t *testing.T) {
	f := &stepTofu{state: "vm.cp[\"100\"]\ndata.health.this\ndata.config.cp[\"100\"]\nmodule.m.data.x.y\nmodule.m.vm.z\n"}
	n, err := ForgetReads(t.TempDir(), nil, f.run, false)
	if err != nil || n != 3 {
		t.Fatalf("forgot %d, %v", n, err)
	}
	rm := f.calls[len(f.calls)-1]
	want := []string{"state", "rm", "-lock=false", "data.health.this", `data.config.cp["100"]`, "module.m.data.x.y"}
	if !slices.Equal(rm, want) {
		t.Errorf("removed with %v, want %v", rm, want)
	}

	f = &stepTofu{state: "vm.cp\n"}
	if n, err := ForgetReads(t.TempDir(), nil, f.run, true); err != nil || n != 0 || len(f.calls) != 1 {
		t.Errorf("with no reads: forgot %d, %v, calls %v", n, err, f.calls)
	}
	f = &stepTofu{state: "data.a.b\n"}
	if _, err := ForgetReads(t.TempDir(), nil, f.run, true); err != nil || slices.Contains(f.calls[len(f.calls)-1], "-lock=false") {
		t.Errorf("against real state the lock was skipped: %v", f.calls)
	}
}

// A plan forgets the copy's cached reads before its first targeted step, as
// the converge does.
func TestAPlanForgetsCachedReadsFirst(t *testing.T) {
	f := &stepTofu{state: "data.health.this\n"}
	if _, err := PlanSteps(StepsInputs{Dir: t.TempDir(), Sequence: sequence}, f.run); err != nil {
		t.Fatal(err)
	}
	firstPlan, rm := -1, -1
	for i, c := range f.calls {
		if c[0] == "plan" && firstPlan < 0 {
			firstPlan = i
		}
		if c[0] == "state" && c[1] == "rm" {
			rm = i
		}
	}
	if rm < 0 || rm > firstPlan {
		t.Errorf("the cached read was forgotten at call %d, the first plan was at %d", rm, firstPlan)
	}
}

// fakeOnPath puts a tofu first on PATH that reports its directory, an
// environment variable and its arguments, writes to stderr, and fails when
// asked to.
func fakeOnPath(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\npwd\necho \"marker=$EXEC_MARKER\"\necho \"args=$*\"\necho 'Error: from tofu' >&2\n[ \"$1\" = fail ] && exit 3\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "tofu"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Exec runs tofu where it is told, with the environment it is given - or the
// caller's own when given none - and captures both streams rather than
// printing them, because what tofu prints can name a vault value.
func TestExecRunsInItsDirWithItsEnvAndCapturesBoth(t *testing.T) {
	fakeOnPath(t)
	dir := t.TempDir()
	t.Setenv("EXEC_MARKER", "inherited")

	out, errb, err := Exec(dir, nil, "plan", "-input=false")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{dir, "marker=inherited", "args=plan -input=false"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("stdout does not carry %q:\n%s", want, out)
		}
	}
	if !strings.Contains(string(errb), "Error: from tofu") || strings.Contains(string(out), "Error: from tofu") {
		t.Errorf("stderr was not captured apart from stdout: out %q, err %q", out, errb)
	}

	out, _, err = Exec(dir, append(os.Environ(), "EXEC_MARKER=given"), "fail")
	if err == nil {
		t.Error("tofu's failure was not returned")
	}
	if !strings.Contains(string(out), "marker=given") {
		t.Errorf("the environment given was not used:\n%s", out)
	}
}
