package run

import (
	"slices"
	"testing"
)

// The property, not the diff.
//
// The test below asserts -refresh=false is present in the destroy's arguments.
// That is a change detector: it passes whatever the teardown actually does, and
// it passed throughout the period when the teardown could not destroy a stopped
// machine. The property it failed to guard is this one - a destroy knows what
// the machines are really doing - and nothing asserted it, so removing it was
// invisible.
//
// This asserts the sequence, not the arguments, because the first attempt at
// this test made the same mistake one layer down: it checked that
// refreshMachinesArgs was correct, which said nothing about whether anything
// called it. Deleting the call - the exact regression - failed no test.
func TestTheTeardownLearnsWhatTheMachinesAreDoing(t *testing.T) {
	steps := TeardownSteps()

	refreshAt, destroyAt := -1, -1
	for i, s := range steps {
		if slices.Contains(s.args, "-refresh-only") {
			refreshAt = i
		}
		if slices.Contains(s.args, "destroy") {
			destroyAt = i
		}
	}

	if refreshAt < 0 {
		t.Fatal("the teardown never refreshes the machines before destroying them.\n\n" +
			"It then works from whatever state last recorded. That state said \"running\" " +
			"for five VMs that had been stopped, and the provider waited for a transition " +
			"that had already happened - indefinitely, on an idle hypervisor. See #146.")
	}
	if destroyAt < 0 {
		t.Fatal("the teardown does not destroy anything")
	}
	if refreshAt > destroyAt {
		t.Errorf("the refresh is step %d and the destroy is step %d: learning what the "+
			"machines are doing after removing them is not learning it", refreshAt, destroyAt)
	}

	// Confined, or it reads the cluster-health gate and does not come back.
	if !slices.Contains(steps[refreshAt].args, "-target="+machinesAddress) {
		t.Errorf("the refresh %v is not confined to the machines.\n\n"+
			"An untargeted refresh reads every data source in the configuration, "+
			"including the cluster-health gate, which hung a teardown for ninety "+
			"minutes. See #92.", steps[refreshAt].args)
	}

	// Best-effort, or a hypervisor that cannot be refreshed becomes an estate
	// that cannot be torn down - which is the same class of failure as the one
	// being fixed.
	if !steps[refreshAt].bestEffort {
		t.Error("the refresh is not best-effort, so failing to read the machines now " +
			"prevents destroying them at all")
	}
}

// A teardown must not ask the cluster how it feels.
//
// A destroy plan refreshes before it plans, and refreshing reads every data
// source in the configuration - including data.talos_cluster_health. Measured
// on a real teardown: ninety minutes, five healthy nodes still running, nothing
// destroyed, and the data source's own `timeouts = { read = "10m" }` did not
// bound it. So this is not a slow question, it is an unbounded one, asked by
// the one operation that has no use for the answer.
func TestDestroyDoesNotRefresh(t *testing.T) {
	args := destroyArgs()
	if !slices.Contains(args, "-refresh=false") {
		t.Fatalf("destroy args %v do not skip the refresh.\n\n"+
			"Without it the plan reads data.talos_cluster_health, which asks whether "+
			"the cluster is well - of a command whose entire job is to make it not "+
			"exist. See #92.", args)
	}
}

// The other three are what make it a teardown rather than a prompt, and -json
// is what stops it printing the estate's certificate authorities.
func TestDestroyIsNonInteractiveAndSummarised(t *testing.T) {
	args := destroyArgs()
	for _, want := range []string{"destroy", "-input=false", "-auto-approve", "-json"} {
		if !slices.Contains(args, want) {
			t.Errorf("destroy args %v are missing %q", args, want)
		}
	}
}
