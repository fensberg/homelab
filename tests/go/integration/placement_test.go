//go:build integration

package integration_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Where everything actually runs, asserted rather than audited by hand.
//
// tests/go/repo/workload_placement_test.go checks what the manifests ASK for.
// This checks what the cluster DID, and the two are different questions: a
// Helm value at a path the chart does not read is accepted in silence, a
// `preferred` affinity can be satisfied badly and never revisited, and a
// component Talos owns can carry whatever Talos decided. None of those is
// visible from the repository.
//
// It exists because the first audit of this - run by hand, once, after #237
// merged - found two things nothing was watching for. Running it by hand again
// next time is the failure this repository keeps having to repair.

// A NODE NAME IS A SECRET HERE. Node names are `<site>-cp-100`, and the site
// name must never reach a log or the repository. Nothing below prints one:
// every node becomes its role plus its host octet, and every failure message
// is passed through the redactor first. Static pods are named
// `kube-apiserver-<node>`, so this matters for pod names too.
type placement struct {
	role   map[string]string // node name -> "control-plane" | "worker"
	short  map[string]string // node name -> "cp-100" | "wk-200"
	redact func(string) string
	pods   []corev1.Pod
}

func readPlacement(t *testing.T) placement {
	t.Helper()
	opts := k8s.NewKubectlOptions("", kubeconfig(t), "")

	p := placement{role: map[string]string{}, short: map[string]string{}}
	nodes := k8s.GetNodes(t, opts)
	require.NotEmpty(t, nodes, "the cluster reports no nodes, so nothing below asserts anything")

	for _, n := range nodes {
		role := "worker"
		prefix := "wk"
		if _, ok := n.Labels["node-role.kubernetes.io/control-plane"]; ok {
			role, prefix = "control-plane", "cp"
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

	pods, err := k8s.ListPodsE(t, opts, metav1.ListOptions{})
	require.NoError(t, err, "listing pods across every namespace")
	for _, pod := range pods {
		switch pod.Status.Phase {
		case corev1.PodRunning, corev1.PodPending:
			p.pods = append(p.pods, pod)
		}
	}
	require.NotEmpty(t, p.pods, "no running pods, so nothing below asserts anything")
	return p
}

// where names a pod without naming a machine.
func (p placement) where(pod corev1.Pod) string {
	node := p.short[pod.Spec.NodeName]
	if node == "" {
		node = "UNSCHEDULED"
	}
	return fmt.Sprintf("%s  %s/%s", node, pod.Namespace, p.redact(pod.Name))
}

func (p placement) onControlPlane(pod corev1.Pod) bool {
	return p.role[pod.Spec.NodeName] == "control-plane"
}

// talosOwned is the namespace whose contents this estate does not write.
//
// Everything in it is rendered by Talos from the machine config and reconciled
// back if edited, so its sizing and placement are Talos's decisions, not ours.
// Naming it here rather than testing "not ours" some other way keeps the line
// explicit: the moment something of ours lands in kube-system, this stops
// covering it and somebody has to say so.
const talosOwned = "kube-system"

// The state database is the one workload of ours that stays on the control
// planes, and it is identified by the label CloudNativePG puts on its own
// instance pods rather than by a namespace name - the namespace comes from a
// vault value and must not be written down here.
const cnpgInstanceLabel = "cnpg.io/cluster"

// The property #237 exists to deliver: the control plane carries only
// control-plane work.
//
// Asserted against the cluster rather than the manifests because the manifests
// cannot see this. Four of the five workloads moved by Helm values, one by a
// kustomize patch, and a value at a path a chart does not read is accepted
// without an error - so "the manifest says worker" and "the pod is on a
// worker" are genuinely separate facts.
func TestOnlyTheStateDatabaseRunsOnAControlPlane(t *testing.T) {
	t.Parallel()
	p := readPlacement(t)

	var trespassers []string
	for _, pod := range p.pods {
		if !p.onControlPlane(pod) || pod.Namespace == talosOwned {
			continue
		}
		if _, isDatabase := pod.Labels[cnpgInstanceLabel]; isDatabase {
			continue
		}
		trespassers = append(trespassers, p.where(pod))
	}
	sort.Strings(trespassers)

	assert.Empty(t, trespassers, `%d pod(s) of ours are on a machine holding quorum:

  %s

The control plane is meant to carry control-plane work and the state database,
whose volumes are pinned to those nodes by OpenEBS Local PV Hostpath. Anything
else there is unreserved memory beside etcd, and it is what an integration run
took when it killed a control plane (#236).

If this is a new workload, give it the anti-control-plane affinity the others
carry. If a chart moved, check the values path still exists - a Helm value at a
path nothing reads is accepted in silence.`,
		len(trespassers), strings.Join(trespassers, "\n  "))
}

// besteffortExemptions are the pods that carry no requests and stay that way,
// each with the reason it cannot be fixed here.
//
// A list rather than "skip kube-system", because kube-system is where the
// cluster's own machinery lives and most of it IS sized. Exempting the whole
// namespace would hide a Talos regression as readily as it hides this.
var besteffortExemptions = map[string]string{
	"kube-proxy": "Talos renders and reconciles the kube-proxy DaemonSet, and its " +
		"machine-config surface (cluster.proxy) exposes only disabled, image, mode " +
		"and extraArgs - there is no resources field to set, so this is not ours. " +
		"It is also on its way out: epoch 03 sets cluster.proxy.disabled because " +
		"Cilium replaces kube-proxy.",
}

// BestEffort is the first cgroup the OOM controller reaches for, which is how
// a thirteen-minute integration run died (#234) and how five infrastructure
// pods were found to be the estate's own default victims (#237).
func TestNoPodIsBestEffortWithoutASayingWhy(t *testing.T) {
	t.Parallel()
	p := readPlacement(t)

	var unexplained []string
	for _, pod := range p.pods {
		if pod.Status.QOSClass != corev1.PodQOSBestEffort {
			continue
		}
		exempt := false
		for prefix := range besteffortExemptions {
			if strings.HasPrefix(pod.Name, prefix) {
				exempt = true
				break
			}
		}
		if !exempt {
			unexplained = append(unexplained, p.where(pod))
		}
	}
	sort.Strings(unexplained)

	assert.Empty(t, unexplained, `%d pod(s) request nothing at all:

  %s

A pod with no requests is QoS class BestEffort, which puts it first in the
kubelet's eviction order and first in the OOM controller's. Give it requests -
no limits - or add it to besteffortExemptions with the reason it cannot have
them. An omission and a considered exemption must not look the same from here.`,
		len(unexplained), strings.Join(unexplained, "\n  "))
}

// priorityExemptions are the pods deliberately left unclassified, and until
// when.
var priorityExemptions = map[string]string{
	cnpgInstanceLabel: "The state database. Setting priorityClassName on a " +
		"CloudNativePG Cluster changes the instance pod spec, which CNPG answers " +
		"with a rolling restart and a switchover of the database holding this " +
		"estate's OpenTofu state. The control-plane taint needs a toleration on " +
		"that same spec, so both are done in one edit and one restart rather than " +
		"two. Remove this when the taint lands.",
}

// An unclassified pod is priority zero, below every class this estate
// declares - so it is not merely last, it is below the work deliberately
// marked as able to wait.
func TestEveryPodHasAPriorityClass(t *testing.T) {
	t.Parallel()
	p := readPlacement(t)

	var unclassified []string
	for _, pod := range p.pods {
		if pod.Spec.PriorityClassName != "" {
			continue
		}
		exempt := false
		for label := range priorityExemptions {
			if _, ok := pod.Labels[label]; ok {
				exempt = true
				break
			}
		}
		if !exempt {
			unclassified = append(unclassified, p.where(pod))
		}
	}
	sort.Strings(unclassified)

	assert.Empty(t, unclassified, `%d pod(s) have no priority class:

  %s

Priority zero is below critical, interactive and batch alike. Give it one of
those, or record it in priorityExemptions with the reason and the condition
that removes the exemption.`,
		len(unclassified), strings.Join(unclassified, "\n  "))
}

// Cluster DNS runs two replicas so that losing one machine does not take DNS
// with it. That only holds if they are on different machines.
//
// Talos asks for exactly this and asks for it softly: the CoreDNS Deployment
// it renders carries a podAntiAffinity at
// preferredDuringSchedulingIgnoredDuringExecution, weight 100, over
// kubernetes.io/hostname. Preferred means the scheduler will co-locate them
// when it has a reason to, and IgnoredDuringExecution means it never revisits
// the decision - so a pair placed together during bootstrap, when one node was
// Ready, stays together for the life of the cluster with nothing reporting it.
//
// That is exactly what the first placement audit found: both replicas on the
// same control plane, months after there were five nodes to choose from.
func TestClusterDNSIsNotAllOnOneNode(t *testing.T) {
	t.Parallel()
	p := readPlacement(t)

	nodes := map[string]bool{}
	replicas := 0
	for _, pod := range p.pods {
		if pod.Namespace != talosOwned || pod.Labels["k8s-app"] != "kube-dns" {
			continue
		}
		replicas++
		nodes[p.short[pod.Spec.NodeName]] = true
	}
	require.NotZero(t, replicas, "no CoreDNS pod carries k8s-app=kube-dns, so this asserts nothing - has Talos renamed the label?")

	if replicas < 2 || len(p.short) < 2 {
		t.Skipf("%d CoreDNS replica(s) across %d node(s): spreading is not available, so there is nothing to assert", replicas, len(p.short))
	}

	placed := make([]string, 0, len(nodes))
	for n := range nodes {
		placed = append(placed, n)
	}
	sort.Strings(placed)

	assert.Greater(t, len(nodes), 1, `all %d CoreDNS replicas are on %s.

Losing that one machine takes cluster DNS with it until the pods are
rescheduled, which is the failure the second replica exists to prevent.

Talos's anti-affinity is `+"`preferred`"+`, not `+"`required`"+`, and it is
IgnoredDuringExecution - so this is not a rule being violated, it is a
preference that was satisfied badly once (most likely at bootstrap, when one
node was Ready) and is never revisited. Restarting the Deployment reschedules
them and is the immediate remedy; it does not stop it recurring, which is why
this test exists rather than a note.`,
		replicas, strings.Join(placed, " and "))
}
