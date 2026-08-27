package harness

import (
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
