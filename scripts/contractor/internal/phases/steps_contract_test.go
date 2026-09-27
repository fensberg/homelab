package phases

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"homelab/contractor/internal/run"
	"homelab/contractor/steps"
)

// Every declared step belongs to a phase the converge runs, in the order the
// converge runs them, and the converge's applies end with the untargeted one.
// A step for a phase nothing dispatches would be a step nothing applies while
// every plan went on planning it.
func TestTheDeclaredStepsAreTheConvergesOwn(t *testing.T) {
	last := -1
	for _, st := range steps.Converge {
		at := slices.Index(ConvergePhases, st.Phase)
		if at < 0 {
			t.Errorf("step %q belongs to %q, which a converge does not run", st.Label, st.Phase)
			continue
		}
		if !slices.Contains(AllPhases, st.Phase) {
			t.Errorf("step %q belongs to %q, which an ignition does not run", st.Label, st.Phase)
		}
		if at < last {
			t.Errorf("step %q is declared after a step of a later phase, so the plan's order is not the converge's", st.Label)
		}
		last = at
	}
	if final := steps.Converge[len(steps.Converge)-1]; len(final.Targets) != 0 {
		t.Errorf("the last step %q is targeted, so the plan of it is not the whole of the change", final.Label)
	}
	plan := steps.Plan()
	if len(plan) != len(steps.Converge) {
		t.Fatalf("the plan walks %d steps and the converge applies %d", len(plan), len(steps.Converge))
	}
	for i, st := range steps.Converge {
		if plan[i].Label != st.Label || !slices.Equal(plan[i].Targets, st.Targets) {
			t.Errorf("step %d is %v in the plan and %v in the converge", i, plan[i], st)
		}
	}
	if len(steps.Of("compute"))+len(steps.Of("cluster")) != len(steps.Converge) {
		t.Error("a step belongs to neither phase that applies steps")
	}
}

// `contractor plan` plans in a copy: the root and its state are copied, the
// state pushed rather than written as a file, and every step walked with a
// refresh. The estate's own state is only ever read.
func TestAPlanWalksTheStepsInACopy(t *testing.T) {
	ctx := run.NewContext(t.TempDir(), "site0")
	if err := os.MkdirAll(ctx.ClusterDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"main.tf": "# config\n", "backend_pg.tf": "# the real backend\n"} {
		if err := os.WriteFile(filepath.Join(ctx.ClusterDir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var real, copied [][]string
	tofu := func(dir string, _ []string, args ...string) ([]byte, []byte, error) {
		if dir == ctx.ClusterDir {
			real = append(real, args)
		} else {
			copied = append(copied, args)
		}
		switch args[0] {
		case "state":
			return []byte(`{"serial": 1}`), nil, nil
		case "show":
			return []byte(`{"format_version":"1.2","resource_changes":[]}`), nil, nil
		}
		return nil, nil, nil
	}
	var pushed string
	push := func(dir string, state []byte) error {
		pushed = dir + ":" + string(state)
		return nil
	}
	if _, err := planSteps(ctx, tofu, push); err != nil {
		t.Fatal(err)
	}
	if len(real) != 1 || strings.Join(real[0], " ") != "state pull" {
		t.Errorf("the estate's own root was asked %v; it may only be read", real)
	}
	if !strings.HasSuffix(strings.SplitN(pushed, ":", 2)[0], filepath.Join(".as-built", "plan")) {
		t.Errorf("the state was pushed to %q", pushed)
	}
	if _, err := os.Stat(filepath.Join(ctx.AsBuiltDir, "plan", "backend_pg.tf")); err == nil {
		t.Error("the copy carries the real backend, so it would write the estate's state")
	}
	plans := 0
	for _, c := range copied {
		if c[0] == "plan" {
			plans++
			if slices.Contains(c, "-refresh=false") {
				t.Errorf("a plan of the estate did not refresh: %v", c)
			}
		}
	}
	if plans != len(steps.Converge) {
		t.Errorf("planned %d steps, and the converge applies %d", plans, len(steps.Converge))
	}
}
