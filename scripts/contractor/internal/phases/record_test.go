package phases

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/contractor/internal/run"
	"homelab/details/asbuilt"
)

// The phase's own decisions: what it tells the operator when the estate is
// not converged, and when it calls a record unfit to publish. The sequence
// itself is details/asbuilt's and is tested there.

func recordContext(t *testing.T) *run.Context {
	t.Helper()
	ctx := run.NewContext(t.TempDir(), "site0")
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
