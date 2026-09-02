//go:build e2e

package e2e_test

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"homelab/tests/harness"
)

// A teardown must work whatever state the machines are in.
//
// Nothing needs to be alive to be demolished, and a demolish that only works
// against running machines fails in exactly the situation it is most likely to
// be reached for: after something has already gone wrong.
//
// This exists because that stopped being true and nothing noticed. The
// teardown was given -refresh=false to stop it reading a cluster-health data
// source that never came back (#92). The destroy then worked from state that
// still said the machines were running, so against stopped VMs the provider
// waited for a transition that had already happened - indefinitely, on an idle
// hypervisor, with no Proxmox task in flight (#146).
//
// The unit test that shipped with that change asserted the flag was present.
// It passed throughout. A test that asserts a change is present is not
// coverage; this asserts the property the change broke.
func TestDemolishRemovesAnEstateWhoseMachinesAreStopped(t *testing.T) {
	site := guard(t)
	buildIgnite(t)

	t.Cleanup(func() { teardown(t, site) })

	for _, phase := range buildOutPhases {
		t.Logf("=== phase: %s ===", phase)
		require.NoErrorf(t, ignite(t, site, "-phase", phase, "-keep-on-failure"),
			"phase %q failed before the machines could be stopped", phase)
	}

	// Stop every machine, then demolish. Stopping through the hypervisor's own
	// API rather than through Talos on purpose: the point is a machine that is
	// off, however it got that way, including having crashed.
	cfg := harness.SiteConfig(t)
	hostname, _ := harness.FirstHypervisor(t)
	for i := 0; i < cfg.ControlPlaneCount; i++ {
		id := cfg.Octet*1000 + 100 + i
		require.NoErrorf(t, stopVM(t, hostname, id), "stopping VM %d before the teardown", id)
	}
	for i := 0; i < cfg.ControlPlaneCount; i++ {
		id := cfg.Octet*1000 + 100 + i
		require.Truef(t, waitForVMState(t, hostname, id, "stopped", 3*time.Minute),
			"VM %d never reported stopped, so this test would prove nothing", id)
	}

	// The teardown, run exactly as a human runs it. The cleanup above would
	// also call it, but a failure here should name this test rather than a
	// cleanup: this is the assertion, not the tidying.
	require.NoError(t, ignite(t, site, "-destroy", "-confirm", site),
		"demolish could not tear down an estate whose machines were stopped")

	for i := 0; i < cfg.ControlPlaneCount; i++ {
		id := cfg.Octet*1000 + 100 + i
		require.Truef(t, vmGone(t, hostname, id),
			"VM %d still exists on the hypervisor after a demolish that reported success", id)
	}
}

// --- the hypervisor's API, directly ----------------------------------------
//
// Deliberately not through the contractor: a test that stopped the machines by
// calling the code under test would prove less than nothing.

func pveRequest(t *testing.T, method, hostname, path string) (int, string) {
	t.Helper()
	site := harness.SiteConfig(t)
	_, ip := harness.FirstHypervisor(t)

	endpoint := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/%s", ip, hostname, path)
	req, err := http.NewRequest(method, endpoint, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization",
		"PVEAPIToken="+site.Hypervisor.TokenID+"="+site.Hypervisor.TokenSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Body = io.NopCloser(strings.NewReader(url.Values{}.Encode()))

	// The provider is configured `insecure = true` against this host for the
	// same reason - there is no trusted certificate yet, recorded in Deferred.
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, // #nosec G402
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func stopVM(t *testing.T, hostname string, id int) error {
	t.Helper()
	code, body := pveRequest(t, http.MethodPost, hostname, fmt.Sprintf("qemu/%d/status/stop", id))
	if code >= 300 {
		return fmt.Errorf("stopping VM %d: HTTP %d: %s", id, code, body)
	}
	return nil
}

func waitForVMState(t *testing.T, hostname string, id int, want string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		code, body := pveRequest(t, http.MethodGet, hostname, fmt.Sprintf("qemu/%d/status/current", id))
		if code < 300 && strings.Contains(body, `"status":"`+want+`"`) {
			return true
		}
		time.Sleep(5 * time.Second)
	}
	return false
}

// A VM that is gone answers 500 with "does not exist", not 200 with a status.
func vmGone(t *testing.T, hostname string, id int) bool {
	t.Helper()
	code, body := pveRequest(t, http.MethodGet, hostname, fmt.Sprintf("qemu/%d/status/current", id))
	return code >= 300 || strings.Contains(body, "does not exist")
}
