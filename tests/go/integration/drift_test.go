//go:build integration

package integration_test

import (
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/stretchr/testify/require"

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
func TestDeployedEstateMatchesTheCode(t *testing.T) {
	opts := harness.TofuOptions(t, nil)

	// -detailed-exitcode: 0 means no changes, 2 means changes are pending,
	// 1 means the plan itself errored. Terratest surfaces the code directly.
	code := terraform.InitAndPlanWithExitCode(t, opts)

	require.NotEqual(t, 1, code, "the plan itself failed; the estate cannot be compared to the code until that is fixed")
	require.Equal(t, 0, code,
		"the deployed estate no longer matches the code. Run `tofu plan` in management/cluster to see what changed - either something was altered outside this repository, or a merged change has not been applied yet.")
}
