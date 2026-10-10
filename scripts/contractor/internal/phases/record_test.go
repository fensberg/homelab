package phases

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/contractor/config"
	"homelab/contractor/internal/capacity"
	"homelab/contractor/internal/run"
	"homelab/details/asbuilt"
)

// The phase's own decisions: what it tells the operator when the estate is
// not converged, and when it calls a record unfit to publish. The sequence
// itself is details/asbuilt's and is tested there.

func recordContext(t *testing.T) *run.Context {
	t.Helper()
	ctx := run.NewContext(t.TempDir(), "site0")
	// A run that reaches the platform root puts the cluster's access in the
	// environment. Registered here so the test puts back what was there.
	for _, name := range platformInputs {
		t.Setenv("TF_VAR_"+name, "")
	}
	for path, body := range map[string]string{
		ctx.ConfigTpl:      `{}`,
		ctx.ConfigRendered: `{}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return ctx
}

// Refusing a record of an unconverged estate says what is pending, in the
// same addresses-and-verbs form as a plan, so the operator knows what to
// converge.
func TestAnUnconvergedEstateIsRefusedWithWhatIsPending(t *testing.T) {
	ctx := recordContext(t)
	plan := `{"format_version": "1.2", "resource_changes": [
	  {"address": "proxmox_vm.cp[\"mill-lane\"]", "mode": "managed", "type": "proxmox_vm", "name": "cp",
	   "change": {"actions": ["update"], "before": {"memory": 1}, "after": {"memory": 2}}}]}`
	tofu := func(dir string, env []string, args ...string) ([]byte, []byte, error) {
		if args[0] == "show" {
			return []byte(plan), nil, nil
		}
		return nil, nil, nil
	}
	err := record(ctx, tofu)
	if err == nil {
		t.Fatal("an unconverged estate was recorded")
	}
	for _, want := range []string{"does not match its config", "change", "memory", "Converge first"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "mill-lane") {
		t.Error("the refusal printed an instance key, which can be a vault value")
	}
}

func TestTheRenderedConfigMustExist(t *testing.T) {
	ctx := recordContext(t)
	_ = os.Remove(ctx.ConfigRendered)
	if err := record(ctx, nil); err == nil || !strings.Contains(err.Error(), "Render did not run") {
		t.Errorf("got %v", err)
	}
}

// A record is unfit to publish if it is not quiet or if a real value survived
// the replacing; values the offline plan computed do not make it unfit.
func TestTheReportFailsExactlyTheUnpublishableRecords(t *testing.T) {
	survived := asbuilt.Finding{Where: "a.b.c", Source: asbuilt.SourceVault + ":name"}
	computed := asbuilt.Finding{Where: "a.b.d", Source: asbuilt.SourceSensitive}
	noisy := []byte(`{"format_version": "1.2", "resource_changes": [
	  {"address": "a.b", "mode": "managed", "type": "a", "name": "b", "change": {"actions": ["update"], "before": {"x": 1}, "after": {"x": 2}}}]}`)
	for name, tc := range map[string]struct {
		res  asbuilt.Result
		fail string
	}{
		"quiet and clean":       {asbuilt.Result{Quiet: true, Rounds: 1}, ""},
		"computed values only":  {asbuilt.Result{Quiet: true, Rounds: 2, After: []asbuilt.Finding{computed}}, ""},
		"not quiet":             {asbuilt.Result{Rounds: asbuilt.MaxRounds, LastPlan: noisy}, "not quiet"},
		"a real value survived": {asbuilt.Result{Quiet: true, Before: []asbuilt.Finding{survived}, After: []asbuilt.Finding{survived}}, "survived"},
		"keyed by a real value": {asbuilt.Result{Quiet: true, Rounds: 1, KeyedByAValue: []string{"a.b"}}, "a resource address holds a real value"},
	} {
		err := report(&tc.res)
		switch {
		case tc.fail == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case tc.fail != "" && (err == nil || !strings.Contains(err.Error(), tc.fail)):
			t.Errorf("%s: got %v, want an error saying %q", name, err, tc.fail)
		}
	}
}

func TestDescribeSourcesCountsByOrigin(t *testing.T) {
	got := describeSources(map[string]int{"a": 2, "b": 1, "c": 1})
	if got != "4 real values (2 a, 1 b, 1 c)" {
		t.Errorf("got %q", got)
	}
}

// clusterOutputs is the cluster root's outputs the platform root is
// configured from, as `tofu output -json` and a state both hold them.
const clusterOutputs = `{"cluster_access": {"sensitive": true, "value": {"host": "https://192.0.2.1:6443"}},
  "kubeconfig": {"sensitive": true, "value": "a kubeconfig"}}`

// scriptedTofu is enough of tofu for a site's record to be taken and saved: a
// quiet estate, a cluster state holding a CA and the cluster's access, a
// platform state holding neither, a throwaway CA, and a quiet offline plan.
func scriptedTofu(dir string, _ []string, args ...string) ([]byte, []byte, error) {
	switch {
	case args[0] == "output":
		return []byte(clusterOutputs), nil, nil
	case args[0] == "state" && filepath.Base(dir) == config.PlatformRoot:
		return []byte(`{"serial": 1, "resources": [{"mode": "managed", "type": "kubernetes_namespace", "name": "database",
		  "instances": [{"attributes": {"id": "database"}}]}], "outputs": {}}`), nil, nil
	case args[0] == "show" && strings.Contains(dir, ".as-built"):
		return []byte(`{"format_version":"1.2","resource_changes":[]}`), nil, nil
	case args[0] == "show":
		return []byte(`{"format_version": "1.2", "resource_changes": [],
		  "prior_state": {"values": {"root_module": {"resources": []}}}}`), nil, nil
	case args[0] == "state" && filepath.Base(dir) == "pki":
		return []byte(`{"resources": [{"type": "talos_machine_secrets", "instances": [{"attributes": {"ca": "made-up-ca"}}]}]}`), nil, nil
	case args[0] == "state":
		return []byte(`{"serial": 1, "resources": [{"mode": "managed", "type": "talos_machine_secrets", "name": "this",
		  "instances": [{"attributes": {"ca": "estate-ca-stand-in"}}]}], "outputs": ` + clusterOutputs + `}`), nil, nil
	}
	return nil, nil, nil
}

// A publishable record is saved where -record-out says, with what it is of.
func TestAPublishableRecordIsSavedWhereAsked(t *testing.T) {
	ctx := recordContext(t)
	ctx.RecordOut = filepath.Join(t.TempDir(), "as-built", "site0")
	for _, root := range ctx.Roots() {
		if err := os.MkdirAll(root.Dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GITHUB_SHA", "0123456789abcdef")
	// The record's last act is to ask the cluster what it holds.
	standIns(t, twoWorkers, oneRunningPod)
	if err := takeRecord(ctx, scriptedTofu); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ctx.RecordOut, capacity.File)); err != nil {
		t.Errorf("a record was saved with no capacity beside it, so a plan would have nothing to hold a change against: %v", err)
	}
	// Both roots, each in its own directory: a record of one would plan half
	// a site and say nothing about the rest.
	for _, root := range ctx.Roots() {
		state, _, meta, err := asbuilt.Read(filepath.Join(ctx.RecordOut, root.Name))
		if err != nil {
			t.Fatal(err)
		}
		if meta.Site != "site0" || meta.Commit != "0123456" || meta.Taken.IsZero() {
			t.Errorf("the %s root's record says it is of %+v", root.Name, meta)
		}
		_, hasCA := state["resources"].([]any)[0].(map[string]any)["type"].(string)
		if !hasCA {
			t.Errorf("the %s root's record holds no state", root.Name)
		}
	}
	if os.Getenv("TF_VAR_kubeconfig") != "a kubeconfig" {
		t.Error("the platform root was recorded without being handed the cluster's access")
	}
}

// In a converge a record that could not be taken is a warning: the apply has
// already landed, and failing here would have the aftermath revert it.
// Anywhere else it is the failure it is.
func TestARecordFailureNeverFailsAConverge(t *testing.T) {
	failing := func(string, []string, ...string) ([]byte, []byte, error) {
		return nil, []byte("Error: the estate is unreachable"), os.ErrDeadlineExceeded
	}
	ctx := recordContext(t)
	if err := takeRecord(ctx, failing); err == nil {
		t.Error("a failed record outside a converge was not an error")
	}
	ctx.Converge = true
	if err := takeRecord(ctx, failing); err != nil {
		t.Errorf("a failed record failed the converge: %v", err)
	}
}

// The commit a record names comes from the run, or from git when there is
// no run.
func TestTheRecordedCommitIsTheRunsOrGits(t *testing.T) {
	t.Setenv("GITHUB_SHA", "fedcba9876543210")
	if got := recordedCommit(); got != "fedcba9" {
		t.Errorf("got %q", got)
	}
	t.Setenv("GITHUB_SHA", "")
	if got := recordedCommit(); strings.Contains(got, "fedcba9") {
		t.Errorf("a stale run commit was used: %q", got)
	}
}
