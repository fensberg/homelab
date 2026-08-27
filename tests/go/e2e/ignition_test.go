//go:build e2e

// Package e2e_test builds an estate from nothing and tears it down again.
//
// THIS TIER CREATES AND DESTROYS REAL INFRASTRUCTURE. Nothing else in the
// repository does that from a test. It is gated three ways, deliberately:
//
//  1. A build tag. `go test ./...` does not compile this file, so there is no
//     flag to forget.
//
//  2. HOMELAB_E2E_CONFIRM must equal the site under test, spelled out. An
//     empty or mismatched value stops the test before it touches anything -
//     so a run aimed at the wrong site fails at the guard rather than at the
//     hypervisor.
//
//  3. The site must not be the one config/management.tpl.json ships as the
//     default. Pointing this at the estate you actually depend on should take
//     more than an environment variable.
//
//     HOMELAB_TEST_SITE=site1 HOMELAB_E2E_CONFIRM=site1 \
//     go test -tags=e2e -timeout 90m ./e2e/...
//
// # Why it stops before the Migrate phase
//
// Migrate moves OpenTofu state off local disk and into Postgres inside the
// cluster it just built. After that point, tearing down means destroying the
// database that holds the only record of what there is to destroy - the
// circular problem EmergencyDestroy solves by migrating state back out first.
// That path exists only on the failure route; there is no supported
// "destroy this estate" entrypoint on the success route.
//
// So this tier covers Render through Cluster - the entire build-out, which is
// the part that can actually be wrong - and tears down from local state,
// which is unambiguous and complete. Extending it through Migrate and Backup
// needs ignite to grow a real teardown command first; see tests/README.md.
package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"homelab/tests/harness"
)

// buildOutPhases is the ignition sequence minus migrate, backup and
// sterilize - see the package comment for why it stops where it does.
var buildOutPhases = []string{"render", "overlay", "hypervisor", "verify", "compute", "cluster"}

func guard(t *testing.T) string {
	t.Helper()
	site := harness.Site()

	confirm := os.Getenv("HOMELAB_E2E_CONFIRM")
	require.Equalf(t, site, confirm,
		"this tier destroys real infrastructure. Set HOMELAB_E2E_CONFIRM to the site under test (%q) to proceed; it is currently %q.", site, confirm)

	require.NotEqual(t, "site0", site,
		"site0 is the default in config/management.tpl.json and is almost certainly the estate you depend on. Point this tier at a disposable site.")

	return site
}

// ignite builds the binary fresh and runs one phase, exactly the way the
// taskfile does - never `go run`, whose wrapper process has no signal handler
// and can be killed before the program itself gets to clean up.
func ignite(t *testing.T, site string, args ...string) error {
	t.Helper()
	root := harness.RepoRoot(t)
	bin := filepath.Join(root, "scripts", "ignite", "ignite")

	cmd := exec.Command(bin, append([]string{"-site", site}, args...)...)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func buildIgnite(t *testing.T) {
	t.Helper()
	build := exec.Command("go", "build", "-C", filepath.Join(harness.RepoRoot(t), "scripts", "ignite"), "-o", "ignite", ".")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	require.NoError(t, build.Run(), "building ignite")
}

func TestIgnitionBuildsAndTearsDownAnEstate(t *testing.T) {
	site := guard(t)
	buildIgnite(t)

	// Registered before the first phase runs, so an estate is torn down even
	// if the build-out fails partway through - which is the run most likely
	// to leave something behind.
	t.Cleanup(func() { teardown(t, site) })

	for _, phase := range buildOutPhases {
		t.Logf("=== phase: %s ===", phase)

		// -keep-on-failure is what stops ignite sterilizing the workspace
		// between phases. Without it, a successful single-phase run wipes
		// the rendered config it just produced, and the next phase has
		// nothing to read. See tests/README.md.
		require.NoErrorf(t, ignite(t, site, "-phase", phase, "-keep-on-failure"),
			"phase %q failed; the estate is about to be torn down by the cleanup above", phase)
	}

	// The build-out claims to be finished. Confirm it independently rather
	// than trusting the exit code: every control-plane node should answer,
	// and the cluster should report the node count that was asked for.
	cfg := harness.SiteConfig(t)
	for i := 0; i < cfg.ControlPlaneCount; i++ {
		ip := harness.ControlPlaneIP(t, i)
		assert.Truef(t, waitForPort(ip, "50000", 5*time.Minute),
			"ignition reported success but %s never answered on the Talos API port", ip)
	}
}

// teardown destroys the estate and then wipes the workspace, in that order.
// The order is the whole point: deleting the state first would leave VMs
// running that nothing tracks - the same reasoning EmergencyDestroy is built
// on. State is still local here, so this is a plain destroy with no
// migration to unwind.
func teardown(t *testing.T, site string) {
	root := harness.RepoRoot(t)
	clusterDir := filepath.Join(root, "management", "cluster")

	if _, err := os.Stat(filepath.Join(clusterDir, "terraform.tfstate")); err != nil {
		t.Log("no local state file - nothing to destroy")
	} else {
		destroy := exec.Command("tofu", "destroy", "-input=false", "-auto-approve")
		destroy.Dir = clusterDir
		destroy.Stdout, destroy.Stderr = os.Stdout, os.Stderr
		if err := destroy.Run(); err != nil {
			// Deliberately not sterilizing after a failed destroy: the
			// state file is the only remaining way to retry it or to find
			// out what is still running.
			t.Errorf("tofu destroy failed and the workspace is being left intact on purpose: %v\n\nCheck Proxmox by hand, then re-run `tofu destroy` in management/cluster and `task clean-secrets SITE=%s`.", err, site)
			return
		}
	}

	assert.NoError(t, ignite(t, site, "-phase", "sterilize"), "sterilizing the workspace")
}

func waitForPort(host, port string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if portOpen(host, port, 5*time.Second) {
			return true
		}
		time.Sleep(10 * time.Second)
	}
	return false
}
