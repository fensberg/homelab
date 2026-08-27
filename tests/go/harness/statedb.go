package harness

import (
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"
)

// StateConnString returns the state database's connection string by reading
// the OpenTofu output that derives it.
//
// Deliberately not rebuilt from parts here. The port, database name and owner
// are already stated twice - once in variables.tf and once in sterilize.go's
// break-glass path, which a contract test holds together. A third hand-written
// copy in the test harness would be a third thing to keep in step, and worse,
// it would be the copy that decides whether the other two are working. Reading
// the output means this asks the estate what its address is rather than
// asserting one.
func StateConnString(t *testing.T) string {
	t.Helper()
	return terraform.OutputRequired(t, TofuOptions(t, nil), "state_conn_str")
}
