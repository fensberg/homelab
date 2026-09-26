package asbuilt

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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
}

func (f *fakeTofu) run(dir string, env []string, args ...string) ([]byte, []byte, error) {
	switch {
	case dir == filepath.Join(f.work, "cluster"):
		f.calls = append(f.calls, "offline: "+strings.Join(args, " "))
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
    {"index_key": "pve-harbour-road", "attributes": {"name": "harbour-road-cp", "ca": "REAL-CLUSTER-CA", "db": "generated-db-password"}}]}
], "outputs": {}}`

const noisyOfflinePlan = `{"format_version": "1.2", "resource_changes": [
  {"mode": "managed", "type": "proxmox_vm", "name": "cp", "index": "unmatched",
   "change": {"actions": ["update"], "after": {"name": "recomputed"}, "after_unknown": {}, "after_sensitive": {}}}]}`

const quietOfflinePlan = `{"format_version": "1.2", "resource_changes": []}`

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
		Root: cluster, Work: filepath.Join(root, ".as-built"), Site: "site0",
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

// The whole sequence: the real CA swapped, the vault value and the generated
// password replaced everywhere including the instance key, the offline copy
// planned until quiet, and nothing real in what was planned against.
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
		"KUBECONFIG=/k", "KUBE_CONFIG_PATH=/k", "TALOSCONFIG=/t", "OP_SERVICE_ACCOUNT_TOKEN=x", "AWS_PROFILE=x",
	}, "site0", "/record/config.json")
	for _, gone := range []string{"TF_ENCRYPTION=real", "TF_VAR_config_path=/real", "PROXMOX_VE_ENDPOINT=x", "KUBECONFIG=/k",
		"KUBE_CONFIG_PATH=/k", "TALOSCONFIG=/t", "OP_SERVICE_ACCOUNT_TOKEN=x", "AWS_PROFILE=x"} {
		if slices.Contains(env, gone) {
			t.Errorf("%s was inherited", gone)
		}
	}
	for _, want := range []string{"PATH=/bin", "TF_VAR_offline=true", "TF_VAR_site=site0", "TF_VAR_config_path=/record/config.json"} {
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
