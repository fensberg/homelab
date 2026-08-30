package phases

import (
	"strings"
	"testing"
)

// The rule this enforces is the one that was asked for: never delete the old
// state file until a new one is confirmed to exist. Everything below is a way
// of getting that wrong, so every one of them is a test.

func names(objs ...string) []string { return objs }

func TestPruneTargets_KeepsTheNewestTwo(t *testing.T) {
	objs := names(
		"20260101-000000.tfstate.age",
		"20260102-000000.tfstate.age",
		"20260103-000000.tfstate.age",
		"20260104-000000.tfstate.age",
	)
	got, err := pruneTargets(objs, "20260104-000000", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"20260101-000000.tfstate.age", "20260102-000000.tfstate.age"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}

// The gate. If the upload that was just made is not in the listing, something
// went wrong between writing and reading it back - and that is the exact
// moment deleting the previous copy would be unrecoverable.
func TestPruneTargets_RefusesWhenTheNewUploadIsAbsent(t *testing.T) {
	objs := names(
		"20260101-000000.tfstate.age",
		"20260102-000000.tfstate.age",
	)
	_, err := pruneTargets(objs, "20260103-000000", 2)
	if err == nil {
		t.Fatal("expected a refusal when the new upload is missing from the listing")
	}
	if !strings.Contains(err.Error(), "20260103-000000") {
		t.Errorf("the error should name the upload it could not find, got: %v", err)
	}
}

// Fewer objects than we intend to keep is the normal state of a young bucket,
// not a problem. Deleting nothing is correct.
func TestPruneTargets_NothingToDoWhenUnderTheLimit(t *testing.T) {
	for _, objs := range [][]string{
		names("20260101-000000.tfstate.age"),
		names("20260101-000000.tfstate.age", "20260102-000000.tfstate.age"),
	} {
		got, err := pruneTargets(objs, strings.TrimSuffix(objs[len(objs)-1], ".tfstate.age"), 2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("with %d object(s) and keep=2, got %v, want nothing deleted", len(objs), got)
		}
	}
}

// latest.tfstate.age is a pointer that is overwritten every run, not a
// generation. Pruning it would delete the thing the restore instructions tell
// you to fetch.
func TestPruneTargets_NeverTouchesLatest(t *testing.T) {
	objs := names(
		"latest.tfstate.age",
		"20260101-000000.tfstate.age",
		"20260102-000000.tfstate.age",
		"20260103-000000.tfstate.age",
	)
	got, err := pruneTargets(objs, "20260103-000000", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, d := range got {
		if d == "latest.tfstate.age" {
			t.Fatal("pruneTargets proposed deleting latest.tfstate.age")
		}
	}
	if strings.Join(got, ",") != "20260101-000000.tfstate.age" {
		t.Errorf("got %v, want only the oldest generation", got)
	}
}

// The listing arrives in whatever order the object store felt like. Sorting
// has to happen here, or "the newest two" is a coin flip.
func TestPruneTargets_IsOrderIndependent(t *testing.T) {
	objs := names(
		"20260103-000000.tfstate.age",
		"20260101-000000.tfstate.age",
		"latest.tfstate.age",
		"20260102-000000.tfstate.age",
	)
	got, err := pruneTargets(objs, "20260103-000000", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(got, ",") != "20260101-000000.tfstate.age" {
		t.Errorf("got %v, want only the oldest generation regardless of listing order", got)
	}
}

// Anything that is not a generation this code wrote is left alone. Deleting an
// unrecognised object in someone else's prefix is not this function's business.
func TestPruneTargets_IgnoresUnrelatedObjects(t *testing.T) {
	objs := names(
		"20260101-000000.tfstate.age",
		"20260102-000000.tfstate.age",
		"20260103-000000.tfstate.age",
		"notes.txt",
		"README",
	)
	got, err := pruneTargets(objs, "20260103-000000", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(got, ",") != "20260101-000000.tfstate.age" {
		t.Errorf("got %v, want only the oldest generation", got)
	}
}

// keep must be at least one, or a bug in a caller turns this into "delete
// everything including what was just uploaded".
func TestPruneTargets_RefusesToKeepNothing(t *testing.T) {
	objs := names("20260101-000000.tfstate.age", "20260102-000000.tfstate.age")
	for _, keep := range []int{0, -1} {
		if _, err := pruneTargets(objs, "20260102-000000", keep); err == nil {
			t.Errorf("keep=%d was accepted; it must never be possible to prune every generation", keep)
		}
	}
}
