package run

import (
	"slices"
	"testing"
)

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
