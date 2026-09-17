//go:build integration

package integration_test

import (
	"strings"
	"testing"

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
