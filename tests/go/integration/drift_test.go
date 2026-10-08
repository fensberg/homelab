//go:build integration

package integration_test

import (
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/stretchr/testify/require"

	"homelab/contractor/config"
	"homelab/tests/harness"
)

// Drift detection: does the estate still match the code that describes it?
//
// This is the one assertion CI genuinely cannot make from a pull request.
// `tofu validate` proves the code resolves and `tofu test` proves the
// invariants hold, but neither can see that somebody resized a VM in the
// Proxmox web UI last Tuesday. A plan against the real state can, and running
// it nightly is what turns "no ClickOps" from a rule people agree to into one
// the repository actually checks.
//
// Plan only - InitAndPlanWithExitCode never applies anything.
// covers: verb:plan
func TestDeployedEstateMatchesTheCode(t *testing.T) {
	// Both of the site's roots: a drift check that planned the machines and
	// not what is on them would call an estate with a hand-edited secret
	// unchanged.
	for _, root := range config.Roots {
		t.Run(root, func(t *testing.T) {
			opts := harness.TofuOptions(t, root, nil)

			// -detailed-exitcode: 0 means no changes, 2 means changes are
			// pending, 1 means the plan itself errored. Terratest surfaces
			// the code directly.
			//
			// Planned as the converge plans it: the cluster root is handed
			// the hardware its state records, or the plan reports the
			// check's own omission as a change to the estate.
			terraform.Init(t, opts)
			if root == config.ClusterRoot {
				harness.HandOverRecordedHardware(t, opts)
			}
			code := terraform.PlanExitCode(t, opts)

			require.NotEqual(t, 1, code, "the plan itself failed; the estate cannot be compared to the code until that is fixed")
			require.Equalf(t, 0, code,
				"the deployed estate no longer matches the code. Run `contractor plan` to see what changed in the %s root - either something was altered outside this repository, or a merged change has not been applied yet.", root)
		})
	}
}
