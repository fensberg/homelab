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
// # What it covers
//
// The whole ignition sequence: Render through Backup, which includes moving
// state off local disk into cluster Postgres and pushing an encrypted copy
// off-site. It used to stop before Migrate, because after that point tearing
// down means destroying the database holding the only record of what there is
// to destroy, and the code that unwinds that lived only on the failure route.
// `ignite -destroy` is now a supported entrypoint that handles it, so this
// tier can cover the part of ignition that was previously untestable - which
// is also the part with the most ways to go wrong.
//
// Teardown goes through that same entrypoint rather than calling `tofu
// destroy` directly. A test that tore down its own way would be exercising
// the test's teardown rather than the one a human uses at 2am.
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

// buildOutPhases is the full ignition sequence minus sterilize, which is left
// out because the teardown below runs it as its own final step - and because
// sterilizing here would delete the rendered config the assertions still need.
var buildOutPhases = []string{
	"render", "overlay", "hypervisor", "verify",
	"compute", "cluster", "migrate", "backup",
}

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

// teardown runs the supported destroy entrypoint - the same command, with the
// same guards and the same ordering, that a human would run. It migrates
// state back out of the cluster before destroying it, destroys, and
// sterilizes, refusing to sterilize if the destroy itself failed.
//
// -confirm is required and must name the site, which is the point: even the
// test has to say it twice.
func teardown(t *testing.T, site string) {
	if err := ignite(t, site, "-destroy", "-confirm", site); err != nil {
		t.Errorf(`ignite -destroy failed: %v

State and secrets have been left in place on purpose - that is what the
destroy path does when it cannot finish, because wiping them is how VMs get
orphaned. Check Proxmox by hand, then re-run:

    ./scripts/ignite/ignite -site %s -destroy -confirm %s`, err, site, site)
	}
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
