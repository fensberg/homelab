package asbuilt

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"homelab/details/platform"
)

// fakeTofu answers each tofu command from a script, and records what was
// asked, so the sequence is tested without an estate.
type fakeTofu struct {
	t     *testing.T
	work  string
	calls []string
	// offlinePlans are served in turn to the offline copy's `show -json`.
	offlinePlans []string
	// failOffline makes the offline copy's plan fail with this stderr.
	failOffline string
	realPlan    string
	state       string
	// offlineEnv is the environment the offline copy last ran in.
	offlineEnv []string
}

func (f *fakeTofu) run(dir string, env []string, args ...string) ([]byte, []byte, error) {
	switch {
	case dir == filepath.Join(f.work, "cluster"):
		f.calls = append(f.calls, "offline: "+strings.Join(args, " "))
		f.offlineEnv = env
		if env == nil || !slices.Contains(env, "TF_VAR_offline=true") {
			f.t.Errorf("the offline copy ran without the offline environment: %v", args)
		}
		switch args[0] {
		case "plan":
			if f.failOffline != "" {
				return nil, []byte(f.failOffline), errors.New("exit 1")
			}
		case "show":
			p := f.offlinePlans[0]
			if len(f.offlinePlans) > 1 {
				f.offlinePlans = f.offlinePlans[1:]
			}
			return []byte(p), nil, nil
		}
		return nil, nil, nil
	case dir == filepath.Join(f.work, "pki"):
		f.calls = append(f.calls, "pki: "+strings.Join(args, " "))
		if args[0] == "state" {
			return []byte(`{"resources": [{"type": "talos_machine_secrets", "instances": [{"attributes": {"certs": {"cert": "THROWAWAY-CA"}}}]}]}`), nil, nil
		}
		return nil, nil, nil
	default:
		f.calls = append(f.calls, "real: "+strings.Join(args, " "))
		if env != nil {
			f.t.Errorf("the real root was not run in the caller's environment: %v", args)
		}
		switch args[0] {
		case "show":
			return []byte(f.realPlan), nil, nil
		case "state":
			return []byte(f.state), nil, nil
		}
		return nil, nil, nil
	}
}

const quietRealPlan = `{"format_version": "1.2", "resource_changes": [
  {"address": "data.x.y", "mode": "data", "type": "x", "name": "y", "change": {"actions": ["read"]}}],
  "prior_state": {"values": {"root_module": {"resources": [
    {"values": {"password": "generated-db-password"}, "sensitive_values": {"password": true}}]}}}}`

const realState = `{"serial": 5, "lineage": "l", "resources": [
  {"mode": "managed", "type": "talos_machine_secrets", "name": "this",
   "instances": [{"attributes": {"talos_version": "v1.9.0", "certs": {"cert": "REAL-CLUSTER-CA"}}}]},
  {"mode": "managed", "type": "proxmox_vm", "name": "cp", "instances": [
    {"index_key": "node0", "attributes": {"name": "harbour-road-cp", "ca": "REAL-CLUSTER-CA", "db": "generated-db-password"}}]}
], "outputs": {}}`

const noisyOfflinePlan = `{"format_version": "1.2", "resource_changes": [
  {"mode": "managed", "type": "proxmox_vm", "name": "cp", "index": "unmatched",
   "change": {"actions": ["update"], "after": {"name": "recomputed"}, "after_unknown": {}, "after_sensitive": {}}}]}`

const quietOfflinePlan = NothingPlanned

func takeFixture(t *testing.T) (Inputs, *fakeTofu) {
	t.Helper()
	root := t.TempDir()
	cluster := filepath.Join(root, "management", "cluster")
	for name, body := range map[string]string{
		"main.tf": "# config\n", "backend_pg.tf": "# the real backend\n", ".terraform.lock.hcl": "# lock\n",
		filepath.Join("tests", "a.tftest.hcl"): "# test\n",
	} {
		p := filepath.Join(cluster, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	in := Inputs{
		Root: cluster, Work: filepath.Join(root, ".as-built"), Site: "site0", MachineSecrets: true,
		Template: []byte(`{"sites": {"site0": {"name": "{{ op://site0-shared/identity/name }}"}}}`),
		Rendered: []byte(`{"sites": {"site0": {"name": "harbour-road"}}}`),
	}
	return in, &fakeTofu{t: t, work: in.Work, realPlan: quietRealPlan, state: realState}
}

func count(calls []string, prefix string) int {
	n := 0
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// A record is only taken of a converged estate, and the refusal comes before
// the state is ever read: folding pending changes into a record would
// describe them as built.
func TestARecordIsNotTakenOfAnEstateWithPendingChanges(t *testing.T) {
	in, f := takeFixture(t)
	f.realPlan = `{"format_version": "1.2", "resource_changes": [
	  {"address": "a.b", "mode": "managed", "type": "a", "name": "b", "change": {"actions": ["update"]}}]}`
	_, err := Take(in, f.run)
	var pending *NotConvergedError
	if !errors.As(err, &pending) || string(pending.Plan) != f.realPlan {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(err.Error(), "cannot be recorded as built") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	if count(f.calls, "real: state pull") != 0 {
		t.Error("the state was read although the estate was not converged")
	}
}

// An estate that keys a resource by a real value is not recorded as fit to
// publish, even though the record itself replaces the key: the address is
// what the estate's own plans and logs print, and nothing replaces it there.
func TestAnEstateKeyedByARealValueIsNotPublishable(t *testing.T) {
	in, f := takeFixture(t)
	f.state = strings.Replace(realState, `"index_key": "node0"`, `"index_key": "harbour-road"`, 1)
	f.offlinePlans = []string{quietOfflinePlan}
	res, err := Take(in, f.run)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.KeyedByAValue) != 1 || res.KeyedByAValue[0] != "proxmox_vm.cp" {
		t.Errorf("reported %v", res.KeyedByAValue)
	}
	if !res.Quiet || res.Publishable() {
		t.Errorf("quiet %v, publishable %v: a resource address holding a real value must refuse the record", res.Quiet, res.Publishable())
	}
}

// The whole sequence: the real CA swapped, the vault value and the generated
// password replaced everywhere, the offline copy planned until quiet, and
// nothing real in what was planned against.
func TestARecordIsFoldedUntilQuietAndHoldsNothingReal(t *testing.T) {
	in, f := takeFixture(t)
	f.offlinePlans = []string{noisyOfflinePlan, quietOfflinePlan}
	var said []string
	in.Progress = func(s string) { said = append(said, s) }
	res, err := Take(in, f.run)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Quiet || res.Rounds != 2 || !res.Publishable() {
		t.Errorf("quiet %v after %d rounds, publishable %v", res.Quiet, res.Rounds, res.Publishable())
	}
	if res.Replaced["vault"] == 0 || res.Replaced["talos machine secrets"] == 0 || res.Replaced["sensitive"] == 0 {
		t.Errorf("replaced %v", res.Replaced)
	}
	if len(said) == 0 {
		t.Error("nothing was said about progress")
	}

	written, err := os.ReadFile(filepath.Join(in.Work, "cluster", "terraform.tfstate"))
	if err != nil {
		t.Fatal(err)
	}
	for _, real := range []string{"REAL-CLUSTER-CA", "harbour-road", "generated-db-password"} {
		if strings.Contains(string(written), real) {
			t.Errorf("%q reached the record", real)
		}
	}
	if !strings.Contains(string(written), "THROWAWAY-CA") {
		t.Error("the throwaway CA is not in the record")
	}
	config, _ := os.ReadFile(filepath.Join(in.Work, "cluster", "config.json"))
	if strings.Contains(string(config), "harbour-road") {
		t.Error("the offline copy's config holds the real name")
	}
	if _, err := os.Stat(filepath.Join(in.Work, "cluster", "backend_pg.tf")); err == nil {
		t.Error("the offline copy carries the real backend, so its plan would reach the estate's state")
	}
	if _, err := os.Stat(filepath.Join(in.Work, "cluster", "tests")); err == nil {
		t.Error("the offline copy carries the tests")
	}
	if _, err := os.Stat(filepath.Join(in.Work, "real.tfplan")); err == nil {
		t.Error("the real plan, which holds every real value it touched, was left on disk")
	}
	pki, _ := os.ReadFile(filepath.Join(in.Work, "pki", "main.tf"))
	if !strings.Contains(string(pki), `talos_version = "v1.9.0"`) {
		t.Errorf("the throwaway CA was not generated for the estate's Talos version:\n%s", pki)
	}
	if n := count(f.calls, "offline: plan -refresh=false"); n != 2 {
		t.Errorf("planned offline %d times, want 2 (one noisy, one quiet)", n)
	}
}

// A record that never goes quiet is not publishable, and says so after a
// bounded number of rounds rather than looping.
func TestARecordThatNeverGoesQuietStopsAfterBoundedRounds(t *testing.T) {
	in, f := takeFixture(t)
	f.offlinePlans = []string{noisyOfflinePlan}
	res, err := Take(in, f.run)
	if err != nil {
		t.Fatal(err)
	}
	if res.Quiet || res.Publishable() || res.Rounds != MaxRounds {
		t.Errorf("quiet %v, publishable %v, rounds %d", res.Quiet, res.Publishable(), res.Rounds)
	}
	if n := count(f.calls, "offline: plan -refresh=false"); n != MaxRounds {
		t.Errorf("planned %d times, want %d", n, MaxRounds)
	}
}

// A failed offline plan reports what failed and where, never the detail
// beneath it, which can quote the value that caused it.
func TestAFailedOfflinePlanReportsTheDiagnosticNotTheDetail(t *testing.T) {
	in, f := takeFixture(t)
	f.failOffline = "╷\n│ Error: Invalid value\n│\n│   on versions.tf line 17, in provider \"proxmox\":\n│   17:   api_token = \"leaked-detail\"\n╵\n"
	_, err := Take(in, f.run)
	if err == nil {
		t.Fatal("a failed offline plan was not an error")
	}
	if !strings.Contains(err.Error(), "Error: Invalid value") || !strings.Contains(err.Error(), "on versions.tf line 17") {
		t.Errorf("the diagnostic is missing: %v", err)
	}
	if strings.Contains(err.Error(), "leaked-detail") {
		t.Error("the detail beneath a diagnostic was printed")
	}
}

// What the finished record adds over the one before planning is reported as
// computed, and does not stop it being publishable.
func TestComputedFindingsAreThoseThePlanAdded(t *testing.T) {
	a := Finding{Where: "x.y.a", Source: "sensitive"}
	b := Finding{Where: "x.y.b", Source: "sensitive"}
	res := &Result{Quiet: true, After: []Finding{a, b}}
	if got := res.Computed(); len(got) != 2 || !res.Publishable() {
		t.Errorf("computed %v, publishable %v", got, res.Publishable())
	}
	res.Before = []Finding{a}
	if got := res.Computed(); len(got) != 1 || got[0] != b || res.Publishable() {
		t.Errorf("computed %v, publishable %v", got, res.Publishable())
	}
}

// The offline environment owns every namespace a credential could arrive
// through, including ones it does not set.
func TestTheOfflineEnvironmentCarriesNoInheritedCredential(t *testing.T) {
	env := OfflineEnv([]string{
		"PATH=/bin", "TF_ENCRYPTION=real", "TF_VAR_config_path=/real", "PROXMOX_VE_ENDPOINT=x",
		"KUBECONFIG=/k", "KUBE_CONFIG_PATH=/k", "TALOSCONFIG=/t", "OP_EXAMPLE_TOKEN=x", "AWS_PROFILE=x",
		platform.UnreleasedVariable + "=/a/checkout", platform.ReleaseVariable + "=registry.invalid/a-release",
		platform.DigestVariable + "=sha256:abc", platform.CLIConfigVariable + "=/run/settings",
	}, "site0", "/record/config.json")
	for _, gone := range []string{"TF_ENCRYPTION=real", "TF_VAR_config_path=/real", "PROXMOX_VE_ENDPOINT=x", "KUBECONFIG=/k",
		"KUBE_CONFIG_PATH=/k", "TALOSCONFIG=/t", "OP_EXAMPLE_TOKEN=x", "AWS_PROFILE=x",
		// A checkout's modules are never an estate's, whoever set them.
		platform.UnreleasedVariable + "=/a/checkout"} {
		if slices.Contains(env, gone) {
			t.Errorf("%s was inherited", gone)
		}
	}
	// The release the caller chose is the one thing inherited, by name.
	for _, want := range []string{"PATH=/bin", "TF_VAR_offline=true", "TF_VAR_site=site0", "TF_VAR_config_path=/record/config.json",
		platform.ReleaseVariable + "=registry.invalid/a-release", platform.DigestVariable + "=sha256:abc", platform.CLIConfigVariable + "=/run/settings"} {
		if !slices.Contains(env, want) {
			t.Errorf("%s is missing", want)
		}
	}
	if slices.ContainsFunc(OfflineEnv(nil, "site0", ""), func(s string) bool { return strings.HasPrefix(s, "TF_VAR_config_path") }) {
		t.Error("a config path was set where none was given")
	}
}

// The Talos version comes from the state and is written into a file, so
// anything that is not a version is refused rather than spliced in.
func TestATalosVersionThatIsNotAVersionIsNotWrittenIntoAFile(t *testing.T) {
	in, f := takeFixture(t)
	if _, err := throwawayMachineSecrets(in, f.run, `v1" } resource "x" "y" {`); err == nil {
		t.Error("a talos_version carrying HCL was accepted")
	}
}

func TestQuietCountsOutputs(t *testing.T) {
	for plan, want := range map[string]bool{
		`{"output_changes": {"o": {"actions": ["no-op"]}}}`:                     true,
		`{"output_changes": {"o": {"actions": ["update"]}}}`:                    false,
		`{"resource_changes": [{"change": {"actions": ["delete", "create"]}}]}`: false,
	} {
		if got := Quiet(mustDecode(t, plan)); got != want {
			t.Errorf("Quiet(%s) = %v", plan, got)
		}
	}
}

func TestErrorSummaryWithNoDiagnostic(t *testing.T) {
	if !strings.Contains(ErrorSummary([]byte("something odd")), "no diagnostic") {
		t.Error("an error with no diagnostic reported nothing")
	}
}

// Pending names types and outputs, never an instance key.
func TestPendingNamesTypesNeverKeys(t *testing.T) {
	got, err := Pending([]byte(`{"resource_changes": [
	  {"type": "proxmox_vm", "name": "cp", "index": "pve-north", "change": {"actions": ["update"]}},
	  {"type": "proxmox_vm", "name": "cp", "index": "pve-south", "change": {"actions": ["update"]}},
	  {"type": "data_x", "name": "r", "change": {"actions": ["read"]}}],
	  "output_changes": {"kubeconfig": {"actions": ["update"]}, "same": {"actions": ["no-op"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "output.kubeconfig,proxmox_vm.cp" {
		t.Errorf("got %v", got)
	}
	if _, err := Pending([]byte("not json")); err == nil {
		t.Error("a plan that is not JSON was accepted")
	}
}

// The copy must sit at the root's depth, or its "${path.module}/../../"
// references reach the wrong files.
func TestTheRootIsCopiedOnlyToItsOwnDepth(t *testing.T) {
	root := t.TempDir()
	from := filepath.Join(root, "management", "cluster")
	_ = os.MkdirAll(from, 0o700)
	if err := copyRoot(from, filepath.Join(root, ".as-built", "deeper", "plan")); err == nil {
		t.Error("a copy one level too deep was made")
	}
	if err := copyRoot(from, filepath.Join(root, ".as-built", "plan")); err != nil {
		t.Errorf("a copy at the right depth was refused: %v", err)
	}
}

// A root with no Talos machine secrets is recorded without a throwaway CA, and
// its offline plan is given the variables it was handed: that is the platform
// root, configured from the cluster root's outputs.
func TestARootWithoutMachineSecretsIsRecordedWithItsVariables(t *testing.T) {
	in, f := takeFixture(t)
	in.MachineSecrets = false
	in.Vars = map[string]string{"door": "a stand-in", "way_in": `{"address":"a"}`}
	f.state = `{"serial": 1, "lineage": "l", "resources": [
	  {"mode": "managed", "type": "kubernetes_namespace", "name": "database", "instances": [{"attributes": {"id": "database"}}]}
	], "outputs": {}}`
	f.offlinePlans = []string{quietOfflinePlan}
	res, err := Take(in, f.run)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Publishable() {
		t.Errorf("quiet %v, before %v", res.Quiet, res.Before)
	}
	if count(f.calls, "pki:") != 0 {
		t.Errorf("a throwaway CA was generated for a root that holds no machine secrets: %v", f.calls)
	}
	for _, want := range []string{"TF_VAR_way_in={\"address\":\"a\"}", "TF_VAR_door=a stand-in"} {
		if !slices.Contains(f.offlineEnv, want) {
			t.Errorf("the offline plan was not given %q", want)
		}
	}

	// And a root said to hold them that does not is refused, not recorded.
	in.MachineSecrets = true
	if _, err := Take(in, f.run); err == nil || !strings.Contains(err.Error(), "not the state of a site") {
		t.Errorf("a cluster root with no CA was recorded: %v", err)
	}
}

func TestOutputVarsReadsStringsAsThemselvesAndTheRestAsJSON(t *testing.T) {
	state := mustDecode(t, `{"outputs": {
	  "door": {"value": "raw: yaml"},
	  "way_in": {"value": {"address": "a", "key": "k"}},
	  "empty": {"value": null}}}`)
	got, err := OutputVars(state, "door", "way_in")
	if err != nil {
		t.Fatal(err)
	}
	if got["door"] != "raw: yaml" || got["way_in"] != `{"address":"a","key":"k"}` {
		t.Errorf("got %v", got)
	}
	for _, missing := range []string{"absent", "empty"} {
		if _, err := OutputVars(state, missing); err == nil || !strings.Contains(err.Error(), missing) {
			t.Errorf("%s: a record without the output was accepted: %v", missing, err)
		}
	}
}

func TestMergePlansReportsEveryRootsChangesAsOnePlan(t *testing.T) {
	a := `{"format_version": "1.2", "resource_changes": [{"address": "a.one"}], "output_changes": {"x": {"actions": ["update"]}}}`
	b := `{"format_version": "1.2", "resource_changes": [{"address": "b.two"}], "resource_drift": [{"address": "b.drift"}], "output_changes": {"y": {"actions": ["create"]}}}`
	raw, err := MergePlans([]byte(a), []byte(b), []byte(`{"format_version": "1.2"}`))
	if err != nil {
		t.Fatal(err)
	}
	got := mustDecode(t, string(raw))
	changes, _ := got["resource_changes"].([]any)
	outputs, _ := got["output_changes"].(map[string]any)
	drift, _ := got["resource_drift"].([]any)
	if len(changes) != 2 || len(outputs) != 2 || len(drift) != 1 || got["format_version"] != "1.2" {
		t.Errorf("merged to %s", raw)
	}
	if _, err := MergePlans(); err == nil {
		t.Error("no plans at all were reported as a plan")
	}
	if _, err := MergePlans([]byte(a), []byte("not json")); err == nil {
		t.Error("a plan that is not JSON was merged")
	}
}
