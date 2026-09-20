package main

import "testing"

// Only a run that can never produce a wanted result is cancelled.
//
// Two plans on a merged, deleted branch sat queued until an ignition refused
// to start because of them - at the one moment when being stopped is most
// expensive (#181). A missing branch is proof rather than a guess: nothing
// downstream wants a plan of a commit nobody can reach.
func TestDeadBecause_OnlyAMissingBranchIsProof(t *testing.T) {
	if why, dead := deadBecause(false); !dead || why == "" {
		t.Errorf("a run whose branch is gone should be reaped, and should say why; got %q, %v", why, dead)
	}
	if why, dead := deadBecause(true); dead {
		t.Errorf("a run on a branch that still exists was judged dead (%q)", why)
	}
}
