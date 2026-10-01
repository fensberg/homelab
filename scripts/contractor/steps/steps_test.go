package steps

import (
	"errors"
	"homelab/contractor/config"
	"slices"
	"testing"
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
	got, err := ClusterAccess("management/cluster", tofu)
	if err != nil {
		t.Fatal(err)
	}
	if asked != "management/cluster: output" {
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
