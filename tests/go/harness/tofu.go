package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"
)

// TofuOptions builds Terratest options aimed at the management cluster root.
//
// Two things are set here rather than at every call site. TerraformBinary is
// "tofu": Terratest shells out to "terraform" by default, and this project
// has never had that binary - a default left alone would fail with "executable
// file not found" and read like a broken test rather than a wrong tool.
// RetryableTerraformErrors carries the one failure this environment produces
// that genuinely is worth retrying: Proxmox returns a timeout when several
// clones are created at once, which the provider's own documentation calls
// out and handles internally for its resources.
func TofuOptions(t *testing.T, vars map[string]any) *terraform.Options {
	t.Helper()
	if err := StateIsReadable(); err != nil {
		t.Fatal(err)
	}
	return terraform.WithDefaultRetryableErrors(t, &terraform.Options{
		TerraformBinary: "tofu",
		TerraformDir:    filepath.Join(RepoRoot(t), "management", "cluster"),
		Vars:            mergeVars(map[string]any{"site": Site()}, vars),
		NoColor:         true,

		// Never -upgrade. The committed .terraform.lock.hcl decides provider
		// versions, so a test run resolves exactly what a real run resolves;
		// a test that silently floated to a newer provider would be testing
		// something the deployment is not running.
		Upgrade: false,
	})
}

// PlanOnlyOptions is TofuOptions with a fixture config instead of the real
// rendered one, for assertions that only need the plan graph and must never
// touch an estate. Nothing it can be pointed at holds a real credential.
func PlanOnlyOptions(t *testing.T, fixture string, vars map[string]any) *terraform.Options {
	t.Helper()
	opts := TofuOptions(t, vars)
	opts.Vars["config_path"] = filepath.Join("./tests/fixtures", fixture)
	return opts
}

func mergeVars(base, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// StateIsReadable reports whether this process can read the estate's state.
//
// State is encrypted at rest, so a bare tofu invocation cannot read it - that
// is the lock rather than a side effect, and it is working correctly when it
// refuses. What it produces, though, is:
//
//	Error: Failed to load state: Unsupported state file format:
//	This state file is encrypted and can not be read without an encryption
//	configuration
//
// repeated once per test, with no indication of what to do about it. Five of
// the seven integration tests failed that way on 2026-09-05 and the run read
// as a broken suite rather than a missing environment variable (#227).
//
// So the check happens once, before tofu is invoked, and says what to run.
// Deliberately not in PlanOnlyOptions: that plans against a fixture with local
// state and has no business requiring a credential.
//
// A function returning an error rather than one calling t.Fatal, so the
// message itself is testable without a fake testing.T.
func StateIsReadable() error {
	if os.Getenv("TF_ENCRYPTION") != "" {
		return nil
	}
	return fmt.Errorf(`TF_ENCRYPTION is not set, so tofu cannot read this estate's state.

State is encrypted at rest and a bare tofu run cannot decrypt it. The contractor
establishes the passphrase from the vault before any verb, and passes its
environment to whatever it runs - so run the tier through it:

    contractor kubeconfig -site %s -- go test -C tests/go -tags=integration ./integration/...

That needs a vault session (OP_SERVICE_ACCOUNT_TOKEN, or an op signin).`, Site())
}
