//go:build integration

package integration_test

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"homelab/tests/harness"
)

// The machine configuration in management/cluster/talos.tf opens three
// listeners on every control plane. This is whether the machines have them.
//
// WHY THIS EXISTS. The repository declared
// `listen-metrics-urls = "http://0.0.0.0:2381"` and the estate did not have it.
// Nothing said so. `tofu plan` could not: talos_machine_configuration_apply
// records what was sent, not what took effect, so a declaration that the
// machine accepted and did not act on is invisible to a drift check. The only
// symptom was Prometheus having no healthy etcd target, three Slack alerts
// claiming etcd had lost quorum when it had not, and a question that could
// only be answered by a person opening a terminal and running talosctl.
//
// A property this estate declares must not need a human to confirm. That is
// what this file is for.
//
// WHY THE PORT AND NOT THE CONFIG. Reading each node's running machine
// configuration back over the Talos API would test the spelling: a config can
// contain the right key and the service can still not have restarted to act on
// it, which is exactly the failure being guarded. Dialling the port tests the
// thing. It also needs no credential - these are the node addresses the tier
// already reaches for the Talos API check - so the tier does not acquire Talos
// API access, which sits below Kubernetes and can reset a machine.
//
// tests/go/repo/talos_listeners_test.go holds this table and talos.tf
// together, so a listener added there and not dialled here fails the build.
type controlPlaneListener struct {
	// What it is, in the words a failure message should use.
	What string
	// The key under `cluster` in talos.tf's yamlencode patch that opens it.
	// This is what the repo-tier guard matches on.
	Component string
	Port      string
	// The metric to look for when /metrics is servable without a credential,
	// which for etcd it deliberately is - that is why its metrics listener is
	// a separate listener from its client port. Empty means the endpoint
	// authenticates, and reaching the port is all that can be checked here.
	Metric string
}

var controlPlaneListeners = []controlPlaneListener{
	{
		What:      "etcd's metrics listener",
		Component: "etcd",
		Port:      "2381",
		Metric:    "etcd_server_has_leader",
	},
	{
		What:      "the scheduler's metrics listener",
		Component: "scheduler",
		Port:      "10259",
	},
	{
		What:      "the controller-manager's metrics listener",
		Component: "controllerManager",
		Port:      "10257",
	},
}

func TestEveryControlPlaneOpensTheListenersItWasConfiguredFor(t *testing.T) {
	t.Parallel()
	site := harness.SiteConfig(t)
	require.NotZero(t, site.ControlPlaneCount, "this site declares no control planes, so this asserted nothing")

	for _, listener := range controlPlaneListeners {
		for i := 0; i < site.ControlPlaneCount; i++ {
			ip := harness.ControlPlaneIP(t, i)
			t.Run(fmt.Sprintf("%s/%s", listener.Component, ip), func(t *testing.T) {
				t.Parallel()

				if !portOpen(ip, listener.Port, 10*time.Second) {
					assert.Fail(t, "a declared listener is not open", `%s is not listening on %s:%s.

management/cluster/talos.tf configures cluster.%s to open it, so the machine is
not running the configuration this repository declares. Either the config was
never applied to this node, or it was applied and the service did not restart to
act on it - Talos will not restart a service on a whim, and etcd least of all,
because it holds quorum.

This is the cause, not the symptom. If Prometheus is also reporting no target
for this component, or alerting that etcd has lost members, that is downstream
of this line.`, listener.What, ip, listener.Port, listener.Component)
					return
				}

				if listener.Metric == "" {
					return
				}

				// Open is not the same as serving. A port can be held by
				// something that never answers, and "the listener is up" was
				// the claim that needed to be true, not "a socket accepted".
				body, err := scrape(fmt.Sprintf("http://%s/metrics", net.JoinHostPort(ip, listener.Port)))
				require.NoErrorf(t, err, "%s accepted a connection on %s:%s but did not serve /metrics",
					listener.What, ip, listener.Port)
				assert.Truef(t, strings.Contains(body, listener.Metric),
					"%s is serving /metrics on %s:%s but no %s in %d bytes of output, so whatever is on that port is not %s",
					listener.What, ip, listener.Port, listener.Metric, len(body), listener.Component)
			})
		}
	}
}

// scrape reads a metrics endpoint. Bounded and capped: this runs against the
// machines holding quorum, and a test is not a reason to read an unbounded
// body from one.
func scrape(url string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s returned %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return string(body), err
}
