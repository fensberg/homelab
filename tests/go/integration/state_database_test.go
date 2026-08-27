//go:build integration

package integration_test

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"homelab/tests/harness"
)

// buildStateConnStr in sterilize.go rebuilds the state database's address
// from first principles, because the break-glass path it serves has to run at
// the moment Terraform can no longer reach its own state. A unit test proves
// those hard-coded values still match variables.tf. This proves they still
// match the estate - which is the question that actually matters when the
// emergency destroy runs, and the one no amount of reading source can answer.
func TestDerivedStateDatabaseAddressMatchesTheDeployedOne(t *testing.T) {
	t.Parallel()
	site := harness.SiteConfig(t)

	// Derived exactly the way the break-glass path derives it.
	derived := fmt.Sprintf("10.%d.10.100:30432", site.Octet)

	// Reported by the cluster that actually exists.
	deployed := terraform.OutputRequired(t, harness.TofuOptions(t, nil), "state_db_endpoint")

	require.Equal(t, deployed, derived,
		"the emergency destroy would dial %s, but the state database is at %s.\n\nThat path runs only when a deployment has already failed, so a mismatch here surfaces for the first time at the exact moment there is no second chance: state gets migrated out of a cluster that is about to be torn down, to an address that answers nothing.", derived, deployed)

	assert.True(t, portOpen(derived, "", 15*time.Second),
		"nothing is listening on %s. Has Flux finished reconciling CloudNativePG?", derived)
}

// portOpen is the same single-TCP-dial check run.TestPort makes: is anything
// listening. host may carry the port already, in which case port is "".
func portOpen(host, port string, timeout time.Duration) bool {
	addr := host
	if port != "" {
		addr = net.JoinHostPort(host, port)
	}
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
