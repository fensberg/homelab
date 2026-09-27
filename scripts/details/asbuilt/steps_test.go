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
		return []byte(`{"format_version": "1.2", "resource_changes": []}`), nil, nil
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
	first := f.calls[0]
	if first[0] != "apply" || !slices.Contains(first, "-refresh-only") || !slices.Contains(first, "-target=vm.old") || !slices.Contains(first, "-target=vm.cp") {
		t.Errorf("against the estate, the first call was %v", first)
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
