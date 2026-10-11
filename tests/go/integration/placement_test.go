//go:build integration

package integration_test

import (
	"fmt"
	"homelab/contractor/config"
	"sort"
	"strings"
	"testing"

	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Does the cluster agree with what this repository declared?
//
// WHAT THIS IS AND IS NOT, because the first version of this file got the line
// wrong and checked four things where only three were tests.
//
// A test compares reality against something this repository DECLARED. When it
// fails, a file is wrong or was silently not applied, and somebody fixes it in
// a commit. A monitor watches state that nothing declared - a scheduler's
// runtime choice, an upstream component changing - and when that moves there
// is nothing in the repository to fix. Failing a build for the second kind is
// noise, and noise is the direction that gets checks switched off.
//
// So this covers the workloads this repository deploys, and nothing else.
// Whether Talos's own components are well placed, and whether a Deployment's
// replicas drifted onto one node, are real questions and they are epoch 04's -
// see docs/epochs/04-observability.md.
//
// WHY IT CANNOT BE DONE IN THE REPO TIER. tests/go/repo checks that the
// manifests ask for the right thing. It cannot check that the ask arrived: all
// of this is set through Helm values, and a value at a path the chart does not
// read is accepted with no error, no warning and no event. The manifest then
// says the pod is placed and sized, and the pod is neither. This is the same
// question TestDeployedEstateMatchesTheCode asks of OpenTofu, asked of the
// things Flux applies.

// A NODE NAME IS A SECRET HERE. Node names are `<site>-cp-100`, and the site
// name must never reach a log or the repository. Nothing below prints one:
// every node becomes its role plus its host octet, and every failure message
// goes through the redactor first.
type placement struct {
	role   map[string]string
	short  map[string]string
	redact func(string) string
	pods   []corev1.Pod
}

// talosOwned is the namespace this repository does not write.
//
// Everything in it is rendered by Talos from the machine config and reconciled
// back if edited, so nothing in it can fail this file for a reason a commit
// could fix. kube-proxy is the concrete case: it is BestEffort, and Talos's
// `cluster.proxy` config exposes only disabled, image, mode and extraArgs -
// there is no resources field to set.
//
// Excluding it was argued against in the first draft, on the grounds that it
// would hide a regression in Talos's own components as readily as it hides
// kube-proxy. That is true and it is the wrong conclusion: such a regression is
// something to be alerted about, not something to fail an acceptance run over.
const talosOwned = "kube-system"

// The state database is the one workload of ours that stays on the control
// planes, identified by the label CloudNativePG puts on its instance pods
// rather than by a namespace name - the namespace comes from a vault value and
// must not be written down here.
const cnpgInstanceLabel = "cnpg.io/cluster"

func readPlacement(t *testing.T) placement {
	t.Helper()
	opts := k8s.NewKubectlOptions("", kubeconfig(t), "")

	p := placement{role: map[string]string{}, short: map[string]string{}}
	nodes := k8s.GetNodes(t, opts)
	require.NotEmpty(t, nodes, "the cluster reports no nodes, so nothing below asserts anything")

	for _, n := range nodes {
		prefix := "wk"
		role := "worker"
		if _, ok := n.Labels[config.ControlPlaneLabel]; ok {
			prefix, role = "cp", "control-plane"
		}
		parts := strings.Split(n.Name, "-")
		p.role[n.Name] = role
		p.short[n.Name] = prefix + "-" + parts[len(parts)-1]
	}
	p.redact = func(s string) string {
		for name, short := range p.short {
			s = strings.ReplaceAll(s, name, short)
		}
		return s
	}

	all, err := k8s.ListPodsE(t, opts, metav1.ListOptions{})
	require.NoError(t, err, "listing pods across every namespace")
	for _, pod := range all {
		if pod.Namespace == talosOwned {
			continue
		}
		switch pod.Status.Phase {
		case corev1.PodRunning, corev1.PodPending:
			p.pods = append(p.pods, pod)
		}
	}
	require.NotEmpty(t, p.pods,
		"no running pods outside %s, so this whole file is asserting nothing - has Flux reconciled?", talosOwned)
	return p
}

func (p placement) where(pod corev1.Pod) string {
	node := p.short[pod.Spec.NodeName]
	if node == "" {
		node = "UNSCHEDULED"
	}
	return fmt.Sprintf("%s  %s/%s", node, pod.Namespace, p.redact(pod.Name))
}

func (p placement) isDatabase(pod corev1.Pod) bool {
	_, ok := pod.Labels[cnpgInstanceLabel]
	return ok
}

// isNodeExporter reports whether a pod is the monitoring stack's node
// exporter, which is a DaemonSet and is meant to cover every machine.
//
// One rule, used by both tests that care, because there were two and they
// disagreed. TestDeployedWorkloadsAreOffTheControlPlane called three exporter
// pods trespassers while
// TestOnlyTheNodeExporterOfTheStackRunsOnAControlPlane asserted - correctly -
// that one had better be there. A cluster cannot satisfy both, so the tier
// failed on a real estate that was behaving exactly as designed.
//
// Matched by name rather than by label to stay identical to the check that was
// already passing; the namespace is required so this cannot quietly excuse
// something called node-exporter somewhere else.
func isNodeExporter(pod corev1.Pod) bool {
	return pod.Namespace == monitoringNamespace &&
		strings.Contains(pod.Name, "node-exporter")
}

// Every manifest under clusters/ but the state database declares a required
// anti-control-plane affinity. This is whether that arrived.
func TestDeployedWorkloadsAreOffTheControlPlane(t *testing.T) {
	t.Parallel()
	p := readPlacement(t)

	var trespassers []string
	for _, pod := range p.pods {
		if p.role[pod.Spec.NodeName] != "control-plane" || p.isDatabase(pod) {
			continue
		}
		// The deliberate exception, asserted the other way round by
		// TestOnlyTheNodeExporterOfTheStackRunsOnAControlPlane: a node's
		// memory headroom is what this epoch watches, and a control plane
		// nobody measures is the tightest machine in the estate. Exempting it
		// here costs nothing, because that test fails if it is ever absent.
		if isNodeExporter(pod) {
			continue
		}
		trespassers = append(trespassers, p.where(pod))
	}
	sort.Strings(trespassers)

	assert.Empty(t, trespassers, `%d deployed pod(s) are on a machine holding quorum:

  %s

Every manifest under clusters/ but the state database declares a required
anti-control-plane affinity, so this is the affinity not arriving rather than
a scheduling accident - `+"`requiredDuringScheduling`"+` cannot be satisfied by a
control plane.

The likely cause is a Helm values path that no longer exists. A value at a path
the chart does not read is accepted in silence, so the manifest still says
worker while the pod is not on one. Check the path against the pinned chart's
own values.yaml; tests/go/repo/workload_placement_test.go records which version
each was read from.`,
		len(trespassers), strings.Join(trespassers, "\n  "))
}

// Requests are declared in the same values as the placement, through the same
// silently-ignored paths, and their absence is what made five infrastructure
// pods the OOM controller's first choice (#237).
func TestDeployedWorkloadsCarryTheRequestsTheyDeclare(t *testing.T) {
	t.Parallel()
	p := readPlacement(t)

	var besteffort []string
	for _, pod := range p.pods {
		if pod.Status.QOSClass == corev1.PodQOSBestEffort {
			besteffort = append(besteffort, p.where(pod))
		}
	}
	sort.Strings(besteffort)

	assert.Empty(t, besteffort, `%d deployed pod(s) request nothing at all:

  %s

Every one of these declares requests in its manifest, so BestEffort here means
the declaration did not arrive - the same silently-ignored values path as the
affinity above. A BestEffort pod is first in the kubelet's eviction order and
first in the OOM controller's.`,
		len(besteffort), strings.Join(besteffort, "\n  "))
}

// No pod is excused. The state database was, until its instances were made
// again with the class its cluster names.
func TestDeployedWorkloadsCarryThePriorityTheyDeclare(t *testing.T) {
	t.Parallel()
	p := readPlacement(t)

	var unclassified []string
	for _, pod := range p.pods {
		if pod.Spec.PriorityClassName == "" {
			unclassified = append(unclassified, p.where(pod))
		}
	}
	sort.Strings(unclassified)

	assert.Empty(t, unclassified, `%d deployed pod(s) have no priority class:

  %s

Priority zero is below critical, interactive and batch alike. Either the
manifest does not set one - repo-tier tests cover that - or it does and the
value did not arrive. A value that arrived on the object and not on its pods
needs the pods made again.`,
		len(unclassified), strings.Join(unclassified, "\n  "))
}
