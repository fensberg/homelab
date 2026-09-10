package repo

import (
	"strings"
	"testing"
)

// The workload bucket is released before anything can empty or destroy it.
//
// A cluster rebuild is the routine way a new Talos version reaches these
// machines (#97), and a rebuild is a demolish followed by an ignition. So the
// teardown runs across the thing people would most mind losing - a world save,
// or whatever else a workload has accumulated - every time the OS moves.
//
// Two calls in Sterilize keep that safe, and their order is the safety:
//
//	forgetWorkloadBucket   takes it out of state, so the destroy cannot reach it
//	emptyObjectStorage     deletes every object in the state bucket
//
// Reversed, or with the first removed, the destroy reaches a bucket Cloudflare
// refuses to delete while it holds objects - so the teardown stops part-way
// with the machines still running, which is the failure the emptying exists to
// prevent. And the obvious "fix" at that point, from somebody looking at a
// stuck teardown rather than at this file, is to empty it.
//
// See #330 and the storage section of docs/epochs/03-workload.md.
func TestTheWorkloadBucketIsReleasedBeforeAnythingCanDeleteIt(t *testing.T) {
	body := readRepoFile(t, "scripts/contractor/internal/phases/sterilize.go")

	forget := strings.Index(body, "forgetWorkloadBucket(ctx)")
	empty := strings.Index(body, "emptyObjectStorage(ctx)")

	if forget < 0 {
		t.Fatal("Sterilize never calls forgetWorkloadBucket.\n\n" +
			"Without it the destroy tries to delete a bucket that holds the workloads' " +
			"backups. Cloudflare refuses to delete a bucket with objects in it, so the " +
			"teardown stops with the machines still running - and emptying it to get " +
			"past that is how the backups are lost.")
	}
	if empty < 0 {
		t.Fatal("Sterilize never calls emptyObjectStorage, so this test proves nothing.\n\n" +
			"Either the teardown stopped emptying the state bucket or the call was renamed.")
	}

	if forget > empty {
		t.Errorf("Sterilize empties object storage before it releases the workload bucket.\n\n"+
			"Order is the safety here. Releasing it first means a failure to release stops "+
			"short of deleting anything; the other way round, the deletion has already "+
			"happened by the time anyone finds out.\n\n"+
			"  forgetWorkloadBucket at %d, emptyObjectStorage at %d", forget, empty)
	}
}
