//go:build integration

package integration_test

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"homelab/tests/harness"
)

// The tunnel connector, asked of the cluster rather than of the manifest.
//
// WHY THIS TIER. Two things about the tunnel are true only on a running
// cluster, and both failed silently before anybody looked. The pod MTU is
// decided by Cilium at runtime from the node's interfaces, so a value pinned
// in git says nothing about what pods are running with. And a connector that
// cannot reach Cloudflare over QUIC quietly falls back to HTTP/2, which
// carries no UDP: every pod Running, every check green, and the game server
// unreachable through the tunnel it was built for (#455).
//
// covers: integration:tunnel
const tunnelNamespace = "tunnel"

// The cluster is running the MTU this repository pins.
//
// Cilium detects the lowest link MTU on the node unless told otherwise, and
// these nodes carry a tailnet interface at 1280 that pods never leave by. The
// pin is in clusters/bootstrap/cilium-values.yaml; what the cluster believes
// is in its own ConfigMap, and the two are only equal if the manifest was
// re-rendered and re-applied (cilium/cilium#37529).
func TestTheClusterRunsThePinnedPodMTU(t *testing.T) {
	values, err := os.ReadFile(filepath.Join(harness.RepoRoot(t), "clusters", "bootstrap", "cilium-values.yaml"))
	require.NoError(t, err, "reading the Cilium values")
	m := regexp.MustCompile(`(?m)^MTU:\s*(\d+)\s*$`).FindStringSubmatch(string(values))
	require.NotNil(t, m, "clusters/bootstrap/cilium-values.yaml pins no MTU")

	opts := k8s.NewKubectlOptions("", kubeconfig(t), "kube-system")
	cm, err := k8s.GetConfigMapE(t, opts, "cilium-config")
	require.NoError(t, err, "reading the cilium-config ConfigMap")

	require.Equal(t, m[1], cm.Data["mtu"],
		"this repository pins MTU %s and the cluster is running %q.\n\n"+
			"Cilium is detecting its own again, or the render was never applied. Detection here "+
			"picks the tailnet interface at 1280, which is below what quic-go will start a "+
			"connection with, and the tunnel then carries no UDP at all (#455).",
		m[1], cm.Data["mtu"])
}

// The connector is carrying QUIC, not the HTTP/2 fallback.
//
// Private routing carries UDP only over QUIC. cloudflared falls back to HTTP/2
// on its own when the QUIC handshake fails, logs it once, and then looks
// entirely healthy: connections registered, /ready answering 200, the tunnel
// green in Cloudflare's dashboard. The difference is only visible in the QUIC
// client metrics, which count nothing when the fallback is in use.
func TestTheConnectorCarriesQUICRatherThanTheFallback(t *testing.T) {
	opts := k8s.NewKubectlOptions("", kubeconfig(t), tunnelNamespace)
	pods, err := k8s.ListPodsE(t, opts, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=cloudflared"})
	require.NoError(t, err, "listing the connector's pods")
	require.NotEmpty(t, pods, "no cloudflared pod is running, so nothing off the LAN can reach this estate")

	for _, pod := range pods {
		t.Run(pod.Name, func(t *testing.T) {
			tunnel := k8s.NewTunnel(opts, k8s.ResourceTypePod, pod.Name, 0, 2000)
			defer tunnel.Close()
			tunnel.ForwardPort(t)

			client := &http.Client{Timeout: 15 * time.Second}
			resp, err := client.Get(fmt.Sprintf("http://%s/metrics", tunnel.Endpoint()))
			require.NoError(t, err, "reading the connector's metrics")
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err, "reading the connector's metrics body")

			require.Greater(t, quicConnections(t, string(body)), 0.0,
				"%s has opened no QUIC connection, so it is on the HTTP/2 fallback.\n\n"+
					"That fallback carries TCP only. Every UDP route through this tunnel - the game "+
					"server among them - is dead while it holds, and nothing else reports it: the "+
					"pod is Running and Cloudflare shows the tunnel as healthy (#455).", pod.Name)
		})
	}
}

// quicConnections reads quic_client_total_connections out of the exposition
// format. It is registered whether or not QUIC is in use, so the value rather
// than the presence is what distinguishes the fallback.
func quicConnections(t *testing.T, metrics string) float64 {
	t.Helper()
	const name = "quic_client_total_connections"
	var total float64
	found := false
	for _, line := range strings.Split(metrics, "\n") {
		if !strings.HasPrefix(line, name) || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			continue
		}
		found = true
		total += v
	}
	if !found {
		t.Fatalf("the connector's metrics carry no %s at all.\n\n"+
			"Either cloudflared has renamed it, in which case this test is reading nothing and "+
			"has to be rewritten rather than deleted, or the metrics endpoint is not the one "+
			"being scraped.", name)
	}
	return total
}
