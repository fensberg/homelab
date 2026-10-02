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
	"homelab/details/applications"
	"homelab/details/flux"
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
	fixture := config.CorpusFixture(root, "valid.json")

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
	requestFluxReconcile(&run.Context{Root: run.Root{Dir: dir}}, filepath.Join(dir, "kubeconfig"), when)

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
	requestFluxReconcile(&run.Context{Root: run.Root{Dir: dir}}, filepath.Join(dir, "kubeconfig"), time.Now())
}

// fakeKubectlGet puts a kubectl on PATH that answers every `get` with body.
func fakeKubectlGet(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "out.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := "#!/bin/sh\ncat " + filepath.Join(dir, "out.json") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// A source that cannot fetch is named with its own reason, ahead of the
// Kustomization that only says its artifact is missing (#579).
func TestFluxNamesTheSourceThatCannotFetchAheadOfWhatWaitsOnIt(t *testing.T) {
	dir := fakeKubectlGet(t, `{"items":[
	  {"kind":"Kustomization","metadata":{"name":"flux-system","namespace":"flux-system"},
	   "status":{"conditions":[{"type":"Ready","status":"False","message":"Source artifact not found, retrying in 30s"}]}},
	  {"kind":"GitRepository","metadata":{"name":"flux-system","namespace":"flux-system"},
	   "status":{"conditions":[{"type":"Ready","status":"False","message":"failed to checkout: dial tcp: lookup github.com: i/o timeout"}]}}
	]}`)
	err := checkFlux(&run.Context{Root: run.Root{Dir: dir}}, filepath.Join(dir, "kubeconfig"))
	if err == nil {
		t.Fatal("a source that cannot fetch was reported as reconciled")
	}
	line := summariseWait(err)
	src := strings.Index(line, "GitRepository flux-system/flux-system: failed to checkout")
	if src < 0 {
		t.Fatalf("the source's own reason is not in the progress line: %s", line)
	}
	if k := strings.Index(line, "Source artifact not found"); k >= 0 && k < src {
		t.Errorf("the Kustomization's consequence is named before the source's cause: %s", line)
	}
}

// Sources on their own are not a reconciled cluster.
func TestFluxWithOnlySourcesIsNotReconciled(t *testing.T) {
	dir := fakeKubectlGet(t, `{"items":[
	  {"kind":"GitRepository","metadata":{"name":"flux-system","namespace":"flux-system"},
	   "status":{"conditions":[{"type":"Ready","status":"True"}]}}
	]}`)
	err := checkFlux(&run.Context{Root: run.Root{Dir: dir}}, filepath.Join(dir, "kubeconfig"))
	if err == nil || !strings.Contains(err.Error(), "no Kustomizations or HelmReleases") {
		t.Fatalf("a cluster with a source and nothing reading it passed: %v", err)
	}
}

// The advice is true for the run that gives it: re-run from Health only when
// the cluster is kept, and otherwise say how to keep it (#579).
func TestHealthAdvisesOnlyWhatThisRunLeavesPossible(t *testing.T) {
	kept := rerunAdvice(&run.Context{Site: "site0", KeepOnFailure: true})
	if !strings.Contains(kept, "-from health") || strings.Contains(kept, "tears the cluster down") {
		t.Errorf("a kept cluster is not advised to re-run from Health: %s", kept)
	}
	gone := rerunAdvice(&run.Context{Site: "site0"})
	if strings.Contains(gone, "-from health") {
		t.Errorf("a run that tears the cluster down advises re-running from Health: %s", gone)
	}
	if !strings.Contains(gone, "-"+run.KeepOnFailureFlag) {
		t.Errorf("a run that tears the cluster down does not say how to keep it: %s", gone)
	}
}

// An OCI HelmRepository never has a Ready condition, by Flux's design, so it
// is not waited on; its HelmRelease says whether the chart could be pulled. A
// classic HelmRepository is still a source like any other.
func TestAnOCIHelmRepositoryIsNotWaitedOn(t *testing.T) {
	got, err := notReady([]byte(`{"items":[
	  {"kind":"HelmRepository","metadata":{"name":"oci-charts","namespace":"flux-system"},"spec":{"type":"oci"},"status":{}},
	  {"kind":"HelmRepository","metadata":{"name":"classic","namespace":"flux-system"},"spec":{},"status":{}}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0], "HelmRepository flux-system/classic") {
		t.Fatalf("want only the classic repository waited on, got %v", got)
	}
}

// A site given work, for the tests below: a release and the Kustomization
// that runs it, in the application's own namespace.
func siteWithWork(t *testing.T, dir string) *run.Context {
	t.Helper()
	ctx := &run.Context{Root: run.Root{Dir: dir}, RepoRoot: t.TempDir(), Site: "site7"}
	mustWriteFile(t, filepath.Join(ctx.RepoRoot, filepath.FromSlash(applications.SiteFilePath(ctx.Site))), `---
kind: OCIRepository
metadata:
  name: alpha
  namespace: flux-system
---
kind: Kustomization
metadata:
  name: alpha-runs
  namespace: flux-system
spec:
  path: ./`+applications.Dir+`/alpha/production
`)
	return ctx
}

func fluxObject(kind, namespace, name, ready, message string) string {
	return `{"kind":"` + kind + `","metadata":{"name":"` + name + `","namespace":"` + namespace + `"},"status":{"conditions":[{"type":"Ready","status":"` + ready + `","message":"` + message + `"}]}}`
}

const coreReady = `{"kind":"Kustomization","metadata":{"name":"the-core","namespace":"flux-system"},"status":{"conditions":[{"type":"Ready","status":"True"}]}}`

// The build gates on the core and never on the site's work (#581): an
// application that cannot start is reported and does not hold the build, and
// what the site file does not declare is the core and does.
func TestHealthGatesOnTheCoreAndNotOnTheSitesWork(t *testing.T) {
	for name, tc := range map[string]struct {
		items   []string
		gates   string
		waiting int
	}{
		"the core ready, the work not": {[]string{coreReady,
			fluxObject(flux.OCIRepository, "flux-system", "alpha", "False", "the release is not there"),
			fluxObject(flux.Kustomization, "flux-system", "alpha-runs", "False", "waiting on its source"),
			fluxObject(flux.HelmRelease, "alpha", "a-chart-the-release-brought", "False", "install failed")}, "", 3},
		"the core ready, the work running": {[]string{coreReady,
			fluxObject(flux.Kustomization, "flux-system", "alpha-runs", "True", "")}, "", 0},
		"the core not ready": {[]string{
			fluxObject(flux.Kustomization, "flux-system", "the-core", "False", "the core is not up"),
			fluxObject(flux.Kustomization, "flux-system", "alpha-runs", "True", "")}, "the core is not up", 0},
		"something nobody declared, in the core's namespace": {[]string{coreReady,
			fluxObject(flux.Kustomization, "flux-system", "slipped-in", "False", "nobody declared this")}, "nobody declared this", 0},
		"the work's name on another kind": {[]string{coreReady,
			fluxObject(flux.HelmRelease, "flux-system", "alpha", "False", "the name is not the object")}, "the name is not the object", 0},
		"only work, and no core at all": {[]string{
			fluxObject(flux.Kustomization, "flux-system", "alpha-runs", "True", "")}, "no Kustomizations or HelmReleases", 0},
	} {
		dir := fakeKubectlGet(t, `{"items":[`+strings.Join(tc.items, ",")+`]}`)
		ctx := siteWithWork(t, dir)
		err := checkFlux(ctx, filepath.Join(dir, "kubeconfig"))
		switch {
		case tc.gates == "" && err != nil:
			t.Errorf("%s: the build was held: %v", name, err)
		case tc.gates != "" && (err == nil || !strings.Contains(err.Error(), tc.gates)):
			t.Errorf("%s: the build was not held on %q: %v", name, tc.gates, err)
		}
		if err != nil && strings.Contains(err.Error(), "alpha-runs") {
			t.Errorf("%s: the site's work is named among what holds the build: %v", name, err)
		}
		_, waiting, werr := workWaiting(ctx, filepath.Join(dir, "kubeconfig"))
		if werr != nil || len(waiting) != tc.waiting {
			t.Errorf("%s: %d piece(s) of work reported waiting (%v), want %d: %v", name, len(waiting), waiting, tc.waiting, werr)
		}
		// Whatever it finds, saying so is not a failure of anything.
		reportWork(ctx, filepath.Join(dir, "kubeconfig"))
	}
}

// A site file this cannot read holds the build: not knowing what is work is
// not the same as there being none to wait past.
func TestHealthRefusesASiteFileItCannotRead(t *testing.T) {
	dir := fakeKubectlGet(t, `{"items":[`+coreReady+`]}`)
	ctx := siteWithWork(t, dir)
	mustWriteFile(t, filepath.Join(ctx.RepoRoot, filepath.FromSlash(applications.SiteFilePath(ctx.Site))), "kind: Kustomization\nmetadata:\n  namespace: flux-system\n")
	if err := checkFlux(ctx, filepath.Join(dir, "kubeconfig")); err == nil || !strings.Contains(err.Error(), "what work site7 was given") {
		t.Errorf("got %v", err)
	}
	if _, _, err := workWaiting(ctx, filepath.Join(dir, "kubeconfig")); err == nil {
		t.Error("the work was reported from a site file that could not be read")
	}
	reportWork(ctx, filepath.Join(dir, "kubeconfig"))

	// And a cluster that cannot be asked is reported, not fatal.
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := workWaiting(ctx, filepath.Join(dir, "kubeconfig")); err == nil {
		t.Error("a cluster that could not be asked reported its work")
	}
	reportWork(ctx, filepath.Join(dir, "kubeconfig"))
	if _, _, err := coreAndWork(ctx, []byte("not json")); err == nil {
		t.Error("output that is not a list was split into core and work")
	}
}
