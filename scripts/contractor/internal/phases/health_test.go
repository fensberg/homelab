package phases

import (
	"homelab/details/repopath"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"homelab/contractor/config"
	"homelab/contractor/internal/run"
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

// The gate must expect exactly the machines the config says to build.
//
// It did not. expectedNodeCount returned ControlPlaneCount, the comparison at
// the call site is strict equality, and so the first converge that built
// workers reported "5 node(s) joined, expected 3" and halted a cluster that
// was entirely healthy. The gate was right to refuse - it is fail-closed and
// the cluster genuinely did not match what it had been told - but what it had
// been told stopped being true the moment a second machine class existed.
//
// Driven from the corpus's own valid fixture rather than a config written
// here, so the numbers this asserts are the numbers a real plan uses.
func TestExpectedNodeCountCountsEveryMachineClass(t *testing.T) {
	root, err := repopath.Root()
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(root, "management", "cluster", "tests", "fixtures", "valid.json")

	cfg, err := config.LoadRendered(fixture)
	if err != nil {
		t.Fatalf("loading the corpus fixture: %v", err)
	}
	site, found := cfg.Sites["site0"]
	if !found {
		t.Fatal("the corpus fixture no longer has a site0, so this test is asserting nothing")
	}
	if site.WorkerCount == 0 {
		t.Fatal("the corpus fixture has no workers, so this cannot tell a gate that counts them from one that does not")
	}

	ctx := &run.Context{Site: "site0", ConfigRendered: fixture}
	got, err := expectedNodeCount(ctx)
	if err != nil {
		t.Fatalf("expectedNodeCount: %v", err)
	}

	want := site.ControlPlaneCount + site.WorkerCount
	if got != want {
		t.Errorf("the health gate expects %d nodes but the config builds %d.\n\nThe comparison at the call site is strict equality, so a gate that undercounts halts a healthy cluster and one that overcounts waits five minutes for a machine nobody asked for.", got, want)
	}
}

// The converge asks Flux to reconcile before it waits on Flux, sources before
// consumers, with the annotation `flux reconcile` itself uses - run against a
// fake kubectl that records what it was asked.
//
// Without the request the health gate watched a failure from before the
// converge had written the variable it was missing, for its whole timeout.
func TestTheConvergeAsksFluxToReconcileSourcesFirst(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	fake := "#!/bin/sh\necho \"$*\" >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	when := time.Date(2026, 9, 24, 23, 0, 0, 0, time.UTC)
	requestFluxReconcile(&run.Context{ClusterDir: dir}, filepath.Join(dir, "kubeconfig"), when)

	body, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("kubectl was never run: %v", err)
	}
	calls := strings.Split(strings.TrimSpace(string(body)), "\n")
	firstConsumer, lastSource := -1, -1
	for i, c := range calls {
		if !strings.Contains(c, "annotate --overwrite --all -A") ||
			!strings.Contains(c, "reconcile.fluxcd.io/requestedAt=2026-09-24T23:00:00Z") {
			t.Errorf("call %d is not a reconcile request: %q", i, c)
		}
		if strings.Contains(c, "repositories") {
			lastSource = i
		} else if firstConsumer < 0 {
			firstConsumer = i
		}
	}
	for _, kind := range []string{"gitrepositories", "ocirepositories", "kustomizations", "helmreleases"} {
		if !strings.Contains(string(body), kind) {
			t.Errorf("no reconcile was requested for %s", kind)
		}
	}
	if firstConsumer >= 0 && lastSource > firstConsumer {
		t.Errorf("a consumer was asked to reconcile before every source was: %v", calls)
	}
}

// A cluster still being built has no Flux CRDs to annotate. That is not a
// reason to fail the converge - the Flux check that follows waits either way.
func TestAReconcileRequestThatCannotBeMadeDoesNotStopTheConverge(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte("#!/bin/sh\necho 'no matches for kind' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	requestFluxReconcile(&run.Context{ClusterDir: dir}, filepath.Join(dir, "kubeconfig"), time.Now())
}
