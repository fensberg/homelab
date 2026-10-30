package steps

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"homelab/contractor/config"
)

// A phase's steps are its own, in the declared order, and the plan's view of
// the steps is the same list.
func TestOfAndPlanAreTheDeclaredSteps(t *testing.T) {
	compute := Of("compute")
	if len(compute) == 0 || compute[0].Targets[0] != DiskImage {
		t.Errorf("compute's steps are %v", compute)
	}
	for _, s := range compute {
		if s.Phase != "compute" {
			t.Errorf("a %s step is among compute's", s.Phase)
		}
	}
	if len(Of("nothing")) != 0 {
		t.Error("a phase with no steps was given some")
	}
	// Root by root, the plans are the converge's steps and nothing else.
	i := 0
	for _, root := range config.Roots {
		for _, p := range Plan(root) {
			s := Converge[i]
			if s.Root != root || p.Label != s.Label || !slices.Equal(p.Targets, s.Targets) {
				t.Errorf("step %d: the %s root plans %v, and the converge applies %v", i, root, p, s)
			}
			i++
		}
	}
	if i != len(Converge) {
		t.Errorf("the roots plan %d steps between them, and the converge applies %d", i, len(Converge))
	}
}

// The platform root is handed the cluster root's access, read from that
// root's outputs, and a cluster that has not produced it yet is an error
// rather than an empty variable.
func TestClusterAccessIsReadFromTheClusterRootsOutputs(t *testing.T) {
	asked := ""
	tofu := func(dir string, _ []string, args ...string) ([]byte, []byte, error) {
		asked = dir + ": " + args[0]
		return []byte(`{"cluster_access": {"value": {"host": "h"}}, "kubeconfig": {"value": "k"}}`), nil, nil
	}
	got, err := ClusterAccess("the/cluster/root", tofu)
	if err != nil {
		t.Fatal(err)
	}
	if asked != "the/cluster/root: output" {
		t.Errorf("asked %q", asked)
	}
	if got["kubeconfig"] != "k" || got["cluster_access"] != `{"host":"h"}` || len(got) != len(PlatformInputs) {
		t.Errorf("got %v", got)
	}

	for name, out := range map[string]string{
		"no cluster yet":   `{}`,
		"a diagnostic":     "Warning: No outputs found",
		"half of the pair": `{"kubeconfig": {"value": "k"}}`,
	} {
		tofu := func(string, []string, ...string) ([]byte, []byte, error) { return []byte(out), nil, nil }
		if _, err := ClusterAccess("d", tofu); err == nil {
			t.Errorf("%s: the platform root was handed something", name)
		}
	}
	failing := func(string, []string, ...string) ([]byte, []byte, error) { return nil, nil, errors.New("exit 1") }
	if _, err := ClusterAccess("d", failing); err == nil {
		t.Error("a failed read of the outputs was not an error")
	}
}

// The platform root is applied only after the cluster root has finished:
// every cluster step before every platform step, and the cluster root ending
// with its untargeted apply, the only one that includes the health read
// that waits for every node. A platform applied to a cluster that is not
// ready is how an ignition tears itself back down.
func TestThePlatformRootComesAfterTheClusterIsReady(t *testing.T) {
	for _, p := range stepOrderProblems(Converge) {
		t.Error(p)
	}
}

// stepOrderProblems is what is wrong with the order of the converge's steps.
func stepOrderProblems(converge []Step) []string {
	var problems []string
	lastCluster, firstPlatform := -1, -1
	for i, st := range converge {
		switch st.Root {
		case config.ClusterRoot:
			lastCluster = i
		case config.PlatformRoot:
			if firstPlatform < 0 {
				firstPlatform = i
			}
		default:
			problems = append(problems, "step "+st.Label+" names the root "+st.Root+", which is not one of a site's")
		}
	}
	switch {
	case lastCluster < 0 || firstPlatform < 0:
		problems = append(problems, "the converge does not apply both of a site's roots, so one of them is never built")
	case firstPlatform < lastCluster:
		problems = append(problems, "step "+converge[firstPlatform].Label+" applies the platform root before step "+converge[lastCluster].Label+" has finished the cluster root. The platform is applied to a cluster whose nodes are Ready, and only the cluster root's last step makes that true.")
	case len(converge[lastCluster].Targets) != 0:
		problems = append(problems, "the cluster root's last step, "+converge[lastCluster].Label+", is targeted. Only an untargeted apply includes the health read that waits for every node, so the platform root would be applied to a cluster that is not ready.")
	}
	return problems
}

func TestStepOrderProblemsRefusesThePlatformBeforeTheClusterIsReady(t *testing.T) {
	c, p := config.ClusterRoot, config.PlatformRoot
	for name, tc := range map[string]struct {
		steps []Step
		want  string
	}{
		"the converge's own":        {Converge, ""},
		"platform first":            {[]Step{{Root: p, Label: "p"}, {Root: c, Label: "c"}}, "before step"},
		"cluster ends targeted":     {[]Step{{Root: c, Label: "c", Targets: []string{"x"}}, {Root: p, Label: "p"}}, "is targeted"},
		"no platform":               {[]Step{{Root: c, Label: "c"}}, "both of a site's roots"},
		"a root that is not a root": {[]Step{{Root: c, Label: "c"}, {Root: p, Label: "p"}, {Root: "elsewhere", Label: "e"}}, "not one of a site's"},
	} {
		got := strings.Join(stepOrderProblems(tc.steps), "|")
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}
