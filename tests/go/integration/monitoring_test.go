//go:build integration

package integration_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The monitoring stack, asked of the cluster rather than of the manifest.
//
// WHY THIS TIER. Everything the stack is configured with arrives as Helm
// values, and a value at a path the chart does not read is accepted in
// silence - no error, no event. The manifest then says the images are pinned
// by digest and the pods are off the control plane, and neither is true of
// what is running. tests/go/repo can only read the ask; this reads what
// arrived, which is the same question TestDeployedEstateMatchesTheCode asks of
// OpenTofu.
//
// WHAT IT DELIBERATELY DOES NOT ASSERT. Whether any particular alert is
// firing, or how much memory Prometheus is using. Those are the estate's live
// state rather than something this repository declared, which makes them a
// monitor's business - and this epoch is building the monitor.
//
// covers: integration:kube-prometheus-stack

const monitoringNamespace = "monitoring"

// Every container of the stack runs an image pinned by digest.
//
// The digests are set through six different value paths, three of them in
// subcharts, and two of those spell the key differently - `sha` in one,
// `digest` in another. Rendering the chart caught two that were silently
// wrong before this ever ran; this catches the next one, on the cluster.
func TestTheMonitoringStackRunsTheImagesItWasPinnedTo(t *testing.T) {
	t.Parallel()
	opts := k8s.NewKubectlOptions("", kubeconfig(t), monitoringNamespace)

	pods, err := k8s.ListPodsE(t, opts, metav1.ListOptions{})
	require.NoError(t, err, "listing pods in the monitoring namespace")
	require.NotEmpty(t, pods,
		"no pods in %s: has Flux reconciled the kube-prometheus-stack HelmRelease?", monitoringNamespace)

	checked := 0
	for _, pod := range pods {
		containers := append(append([]corev1.Container{}, pod.Spec.Containers...), pod.Spec.InitContainers...)
		for _, c := range containers {
			checked++
			assert.Contains(t, c.Image, "@sha256:",
				"pod %s container %s runs %s, which is a tag rather than a digest.\n\n"+
					"A tag is a mutable pointer: the same name is different software later, and "+
					"git records nothing when it moves. The value that was supposed to pin this "+
					"one is in kube-prometheus-stack.yaml, and a value at a path the chart does "+
					"not read is accepted in silence.", pod.Name, c.Name, c.Image)
		}
	}
	require.NotZero(t, checked, "no containers found, so this asserted nothing")
}

// The stack keeps off the machines holding etcd quorum - except the exporter
// that has to be everywhere.
//
// This is the property the manifest's node affinity asks for, and the one that
// silently does not arrive when a chart moves the key. node-exporter is the
// deliberate exception: a node's memory headroom is what this epoch watches,
// and a control plane nobody measures is the tightest machine in the estate.
func TestOnlyTheNodeExporterOfTheStackRunsOnAControlPlane(t *testing.T) {
	t.Parallel()
	p := readPlacement(t)
	opts := k8s.NewKubectlOptions("", kubeconfig(t), monitoringNamespace)

	pods, err := k8s.ListPodsE(t, opts, metav1.ListOptions{})
	require.NoError(t, err, "listing pods in the monitoring namespace")
	require.NotEmpty(t, pods, "no pods in %s, so this asserted nothing", monitoringNamespace)

	sawExporterOnControlPlane := false
	for _, pod := range pods {
		if pod.Spec.NodeName == "" || p.role[pod.Spec.NodeName] != "control-plane" {
			continue
		}
		if strings.Contains(pod.Name, "node-exporter") {
			sawExporterOnControlPlane = true
			continue
		}
		assert.Fail(t, "a monitoring pod is on a control plane",
			p.redact("%s is running on %s. Monitoring a control plane from a pod on it is how "+
				"a memory spike takes the thing that would have reported it - and this estate's "+
				"control planes are 4 GiB machines holding etcd quorum."),
			pod.Name, pod.Spec.NodeName)
	}
	assert.True(t, sawExporterOnControlPlane,
		"no node-exporter pod is running on a control plane, so the nodes this epoch most "+
			"needs to watch are reporting nothing. Its DaemonSet tolerations are in "+
			"kube-prometheus-stack.yaml.")
}

// The CloudNativePG PodMonitor is actually being scraped.
//
// cloudnative-pg.yaml sets podMonitorEnabled, which creates a PodMonitor in
// the database's own namespace. Prometheus ignores objects outside its own
// release unless the four *SelectorNilUsesHelmValues values are false - so
// this is exactly the arrangement that looks configured and collects nothing.
func TestPrometheusScrapesTheStateDatabase(t *testing.T) {
	t.Parallel()
	opts := k8s.NewKubectlOptions("", kubeconfig(t), monitoringNamespace)

	out, err := k8s.RunKubectlAndGetOutputE(t, opts,
		"get", "prometheus", "-o", "jsonpath={.items[*].spec.podMonitorNamespaceSelector}")
	require.NoError(t, err, "reading the Prometheus resource the operator built")
	assert.Equal(t, "{}", strings.TrimSpace(out),
		"Prometheus selects PodMonitors from %q rather than from every namespace.\n\n"+
			"An empty selector means all namespaces. Anything else and the state database's "+
			"PodMonitor - two namespaces away - is created, accepted and never scraped, which "+
			"looks exactly like a working monitor until somebody asks for the data.",
		strings.TrimSpace(out))
}

// The control plane is scraped, not just the workloads.
//
// Talos binds the scheduler and the controller-manager to localhost and leaves
// etcd's metrics listener off, so a cluster can look thoroughly monitored
// while nothing watches the components that schedule the work or hold its
// state. The manifests declaring the fix live in two tiers and are held
// together by tests/go/repo; this asks the only question that proves it
// arrived, which is whether Prometheus has a live target for each.
//
// A target that exists and is down is the failure this catches: the machine
// configuration was not applied, the port is closed, or the scrape is refused.
func TestPrometheusScrapesTheControlPlane(t *testing.T) {
	opts := k8s.NewKubectlOptions("", kubeconfig(t), monitoringNamespace)
	tunnel := k8s.NewTunnel(opts, k8s.ResourceTypeService, "kube-prometheus-stack-prometheus", 0, 9090)
	defer tunnel.Close()
	tunnel.ForwardPort(t)

	for _, job := range []string{"kube-scheduler", "kube-controller-manager", "kube-etcd"} {
		t.Run(job, func(t *testing.T) {
			up := instantQuery(t, tunnel.Endpoint(), fmt.Sprintf(`sum(up{job=%q})`, job))
			require.Greater(t, up, 0.0,
				"Prometheus has no healthy target for %s.\n\n"+
					"Either the Talos machine configuration exposing it was never converged, or the "+
					"scrape is being refused. The control plane is the part of this cluster nothing "+
					"else reports on: no leader election, no work-queue depth, no etcd health.", job)
		})
	}
}

// instantQuery runs one PromQL query and returns the single scalar it expects,
// or zero when the query matched nothing - which for `up` means no target.
func instantQuery(t *testing.T, endpoint, query string) float64 {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+endpoint+"/api/v1/query", nil)
	require.NoError(t, err)
	q := req.URL.Query()
	q.Set("query", query)
	req.URL.RawQuery = q.Encode()

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	require.NoError(t, err, "querying Prometheus")
	defer resp.Body.Close()

	var answer struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Value []any `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&answer), "decoding Prometheus's answer")
	require.Equal(t, "success", answer.Status, "Prometheus refused the query %q", query)
	if len(answer.Data.Result) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(fmt.Sprint(answer.Data.Result[0].Value[1]), 64)
	require.NoError(t, err, "reading the value Prometheus returned")
	return v
}
