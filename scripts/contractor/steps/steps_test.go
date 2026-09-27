package steps

import (
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
	plan := Plan()
	for i, s := range Converge {
		if plan[i].Label != s.Label || !slices.Equal(plan[i].Targets, s.Targets) {
			t.Errorf("step %d: plan %v, converge %v", i, plan[i], s)
		}
	}
}
