package phases

import (
	"reflect"
	"testing"
)

// The first full ignition reported success over a state database running two
// of its three instances. Nothing was wrong with any single step: the VMs came
// up, Flux reconciled, Postgres answered on its port, and the state migrated
// into it. The cluster was simply not healthy, and no phase was asking.
//
// A port that answers is the weakest possible evidence of a working database -
// it is true from the moment the first instance is up. These functions ask the
// question the run should have been asking all along.

const notReadyList = `{
  "items": [
    {"kind": "Kustomization", "metadata": {"name": "infra-controllers", "namespace": "flux-system"},
     "status": {"conditions": [{"type": "Ready", "status": "True"}]}},
    {"kind": "Kustomization", "metadata": {"name": "infra-configs", "namespace": "flux-system"},
     "status": {"conditions": [{"type": "Ready", "status": "False", "message": "dependency not ready"}]}},
    {"kind": "HelmRelease", "metadata": {"name": "openebs", "namespace": "openebs"},
     "status": {"conditions": [{"type": "Ready", "status": "True"}]}}
  ]
}`

func TestNotReady_NamesOnlyWhatIsNotReady(t *testing.T) {
	got, err := notReady([]byte(notReadyList))
	if err != nil {
		t.Fatalf("notReady: %v", err)
	}
	want := []string{"Kustomization flux-system/infra-configs: dependency not ready"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

// An object that has not been reconciled yet has no Ready condition at all.
// Treating "no verdict" as "ready" is how a gate passes before the thing it
// gates on has started.
func TestNotReady_MissingConditionIsNotReady(t *testing.T) {
	got, err := notReady([]byte(`{"items":[{"kind":"HelmRelease","metadata":{"name":"cnpg","namespace":"cnpg-system"},"status":{}}]}`))
	if err != nil {
		t.Fatalf("notReady: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("an object with no Ready condition must count as not ready, got %v", got)
	}
}

// An empty list means the CRDs exist but nothing has been created yet, which
// is emphatically not "everything is healthy".
func TestNotReady_EmptyListIsReportedAsSuch(t *testing.T) {
	got, err := notReady([]byte(`{"items":[]}`))
	if err != nil {
		t.Fatalf("notReady: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("an empty list has nothing unready in it, got %v", got)
	}
	// The caller distinguishes empty from healthy; see healthReport.
}

func TestNotReady_AllReadyIsEmpty(t *testing.T) {
	got, err := notReady([]byte(`{"items":[{"kind":"Kustomization","metadata":{"name":"flux-system","namespace":"flux-system"},"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}`))
	if err != nil {
		t.Fatalf("notReady: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

func TestNotReady_RejectsGarbage(t *testing.T) {
	if _, err := notReady([]byte("not json")); err == nil {
		t.Error("expected an error for unparsable output")
	}
}

// This is the exact shape the missed failure had: three instances asked for,
// two running, and every other signal green.
func TestDatabaseShortfall_TwoOfThree(t *testing.T) {
	body := `{"items":[{"metadata":{"name":"tofu-state","namespace":"database"},
	  "spec":{"instances":3},"status":{"readyInstances":2,"instances":3}}]}`
	ready, want, err := databaseInstances([]byte(body))
	if err != nil {
		t.Fatalf("databaseInstances: %v", err)
	}
	if ready != 2 || want != 3 {
		t.Errorf("got %d/%d, want 2/3", ready, want)
	}
}

func TestDatabaseShortfall_HealthyIsThreeOfThree(t *testing.T) {
	body := `{"items":[{"metadata":{"name":"tofu-state","namespace":"database"},
	  "spec":{"instances":3},"status":{"readyInstances":3,"instances":3}}]}`
	ready, want, err := databaseInstances([]byte(body))
	if err != nil {
		t.Fatalf("databaseInstances: %v", err)
	}
	if ready != want {
		t.Errorf("got %d/%d, want equal", ready, want)
	}
}

// readyInstances is absent until CNPG has something to report. Absent is zero,
// not "as many as we asked for".
func TestDatabaseShortfall_MissingStatusIsZeroReady(t *testing.T) {
	body := `{"items":[{"metadata":{"name":"tofu-state"},"spec":{"instances":3},"status":{}}]}`
	ready, want, err := databaseInstances([]byte(body))
	if err != nil {
		t.Fatalf("databaseInstances: %v", err)
	}
	if ready != 0 || want != 3 {
		t.Errorf("got %d/%d, want 0/3", ready, want)
	}
}

// No Cluster object at all is a failure, not a vacuous pass - it means Flux
// has not created the database this whole phase exists to wait for.
func TestDatabaseShortfall_NoClusterIsAnError(t *testing.T) {
	if _, _, err := databaseInstances([]byte(`{"items":[]}`)); err == nil {
		t.Error("expected an error when no CNPG Cluster exists")
	}
}
