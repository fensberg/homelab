//go:build integration

package integration_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"homelab/contractor/config"
	"homelab/details/tcp"
	"homelab/tests/harness"
)

// buildStateConnStr in sterilize.go rebuilds the state database's address
// from first principles, because the break-glass path it serves has to run at
// the moment Terraform can no longer reach its own state. The address plan is
// the one place that endpoint is computed. This proves it still matches the
// estate - which is the question that actually matters when the
// emergency destroy runs, and the one no amount of reading source can answer.
func TestDerivedStateDatabaseAddressMatchesTheDeployedOne(t *testing.T) {
	t.Parallel()
	// Derived exactly the way the break-glass path derives it: from the
	// address plan, through the same resolution the contractor uses.
	db := harness.SiteNetwork(t).StateDatabase
	derived := fmt.Sprintf("%s:%d", db.Host, db.Port)

	// Reported by the cluster that actually exists.
	deployed := terraform.OutputRequired(t, harness.TofuOptions(t, config.PlatformRoot, nil), "state_db_endpoint")

	require.Equal(t, deployed, derived,
		"the emergency destroy would dial %s, but the state database is at %s.\n\nThat path runs only when a deployment has already failed, so a mismatch here surfaces for the first time at the exact moment there is no second chance: state gets migrated out of a cluster that is about to be torn down, to an address that answers nothing.", derived, deployed)

	assert.True(t, tcp.Listening(derived, 15*time.Second),
		"nothing is listening on %s. Has Flux finished reconciling CloudNativePG?", derived)
}
