package phases

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"homelab/contractor/config"
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
	// Root by root: a root's steps come together, in the order the roots are
	// applied, and each root ends with its untargeted apply - the plan of
	// which is the whole of the change to that root.
	i := 0
	for _, root := range config.Roots {
		plan := steps.Plan(root)
		if len(plan) == 0 {
			t.Fatalf("the %s root has no steps, so nothing applies it", root)
		}
		if final := plan[len(plan)-1]; len(final.Targets) != 0 {
			t.Errorf("the %s root's last step %q is targeted, so the plan of it is not the whole of the change", root, final.Label)
		}
		for _, p := range plan {
			st := steps.Converge[i]
			if st.Root != root || p.Label != st.Label || !slices.Equal(p.Targets, st.Targets) {
				t.Errorf("step %d is %v in the %s root's plan and %v in the converge", i, p, root, st)
			}
			i++
		}
	}
	if i != len(steps.Converge) {
		t.Fatalf("the plans walk %d steps and the converge applies %d", i, len(steps.Converge))
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
	for _, name := range platformInputs {
		t.Setenv("TF_VAR_"+name, "")
	}
	realRoots := map[string]bool{}
	for _, root := range ctx.Roots() {
		realRoots[root.Dir] = true
		if err := os.MkdirAll(root.Dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for name, body := range map[string]string{"main.tf": "# config\n", "backend_pg.tf": "# the real backend\n"} {
			if err := os.WriteFile(filepath.Join(root.Dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	var real, copied [][]string
	tofu := func(dir string, _ []string, args ...string) ([]byte, []byte, error) {
		if realRoots[dir] {
			real = append(real, args)
		} else {
			copied = append(copied, args)
		}
		switch args[0] {
		case "output":
			return []byte(clusterOutputs), nil, nil
		case "state":
			return []byte(`{"serial": 1}`), nil, nil
		case "show":
			return []byte(`{"format_version":"1.2","resource_changes":[]}`), nil, nil
		}
		return nil, nil, nil
	}
	pushes := 0
	push := func(dir string, state []byte) error {
		pushes++
		if !strings.HasSuffix(dir, filepath.Join(".as-built", "plan")) {
			t.Errorf("the state was pushed to %q", dir)
		}
		// Checked here, while the copy exists: it is removed before the
		// next root's is made.
		if _, err := os.Stat(filepath.Join(dir, "backend_pg.tf")); err == nil {
			t.Error("the copy carries the real backend, so it would write the estate's state")
		}
		return nil
	}
	if _, err := planSteps(ctx, tofu, push); err != nil {
		t.Fatal(err)
	}
	if pushes != len(ctx.Roots()) {
		t.Errorf("%d state(s) were copied, and a site has %d roots", pushes, len(ctx.Roots()))
	}
	for _, asked := range real {
		if got := asked[0]; got != "state" && got != "output" {
			t.Errorf("one of the estate's own roots was asked %v; they may only be read", asked)
		}
	}
	if len(real) == 0 {
		t.Error("the estate's roots were never read, so nothing was planned against them")
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

// A step names one of a site's two roots, and a name that is neither is
// refused rather than run somewhere.
func TestAStepsRootIsOneOfTheSitesTwo(t *testing.T) {
	ctx := run.NewContext(t.TempDir(), "site0")
	for _, name := range platformInputs {
		t.Setenv("TF_VAR_"+name, "")
	}
	tofu := func(string, []string, ...string) ([]byte, []byte, error) { return []byte(clusterOutputs), nil, nil }

	in, err := rootFor(ctx, config.ClusterRoot, tofu)
	if err != nil || in.Dir != ctx.Cluster.Dir {
		t.Errorf("the cluster root: %v, %v", in, err)
	}
	if os.Getenv("TF_VAR_kubeconfig") != "" {
		t.Error("the cluster root was handed its own access")
	}
	in, err = rootFor(ctx, config.PlatformRoot, tofu)
	if err != nil || in.Dir != ctx.Platform.Dir {
		t.Errorf("the platform root: %v, %v", in, err)
	}
	if os.Getenv("TF_VAR_kubeconfig") != "a kubeconfig" || os.Getenv("TF_VAR_cluster_access") == "" {
		t.Error("the platform root was not handed the cluster's access")
	}
	if ctx.Dir != ctx.Cluster.Dir {
		t.Error("asking for a root changed the root the caller's own context runs in")
	}
	if _, err := rootFor(ctx, "elsewhere", tofu); err == nil {
		t.Error("a root that is not one of a site's was given somewhere to run")
	}
	empty := func(string, []string, ...string) ([]byte, []byte, error) { return []byte(`{}`), nil, nil }
	if _, err := rootFor(ctx, config.PlatformRoot, empty); err == nil {
		t.Error("the platform root was handed over with no access to the cluster")
	}
}
