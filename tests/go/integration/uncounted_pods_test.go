//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"homelab/details/capacity"

	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// offered is a pod to offer the cluster and never make: the request is
// judged by everything that judges a real one, and nothing is kept.
func offered(t *testing.T, name, class, memory string) (string, error) {
	t.Helper()
	spec := "  containers:\n    - name: c\n      image: registry.invalid/nothing:0\n"
	if memory != "" {
		spec += "      resources:\n        requests:\n          memory: " + memory + "\n"
	}
	if class != "" {
		spec += "  " + capacity.PriorityClassKey + ": " + class + "\n"
	}
	file := filepath.Join(t.TempDir(), "pod.yaml")
	require.NoError(t, os.WriteFile(file, []byte("apiVersion: v1\nkind: Pod\nmetadata:\n  name: "+name+"\nspec:\n"+spec), 0o600))
	return k8s.RunKubectlAndGetOutputE(t, k8s.NewKubectlOptions("", kubeconfig(t), "default"), "create", capacity.DryRunOnServer, "-f", file)
}

// The cluster refuses a pod it cannot count. tests/go/repo holds the rule to
// having no way round it; this is whether the rule, as the cluster runs it,
// refuses what it is for and lets through what it is not.
func TestTheClusterRefusesAPodItCannotCount(t *testing.T) {
	t.Parallel()

	said, err := offered(t, "reserves-nothing", "interactive", "")
	require.Error(t, err, "a pod that reserves no memory was admitted: %s", said)
	assert.Contains(t, said+err.Error(), "reserves no memory", "it was refused, and not for reserving nothing")

	said, err = offered(t, "has-no-class", "", "8Mi")
	require.Error(t, err, "a pod with no priority class was admitted: %s", said)
	assert.Contains(t, said+err.Error(), "names no priority class", "it was refused, and not for having no class")

	said, err = offered(t, "is-counted", "interactive", "8Mi")
	require.NoError(t, err, "a pod with a reservation and a class was refused: %s", said)
}

// The rule judges a pod as it is made, and one already running is not
// touched. So a pod that would be refused today goes on running until the
// day it is made again, which is the day it matters: a machine restarted, a
// database instance replaced. This is every pod the site runs, the
// system's own included, held to what the rule asks.
func TestEveryPodTheSiteRunsWouldBeAdmittedAgain(t *testing.T) {
	t.Parallel()
	p := readPlacement(t)
	all, err := k8s.ListPodsE(t, k8s.NewKubectlOptions("", kubeconfig(t), ""), metav1.ListOptions{})
	require.NoError(t, err, "listing pods across every namespace")

	var refused []string
	for _, pod := range all {
		if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodPending {
			continue
		}
		var why []string
		if pod.Spec.PriorityClassName == "" {
			why = append(why, "no priority class")
		}
		for _, c := range pod.Spec.Containers {
			if _, reserves := c.Resources.Requests[corev1.ResourceMemory]; !reserves {
				why = append(why, "a container that reserves no memory")
				break
			}
		}
		if len(why) > 0 {
			refused = append(refused, p.where(pod)+": "+strings.Join(why, ", "))
		}
	}
	sort.Strings(refused)
	assert.Empty(t, refused, `%d pod(s) the site runs would be refused if they were made again:

  %s

Each goes on running until something replaces it, and is then refused. Give
it a reservation and a class where it is declared; if it has them there and
not here, the pods need making again.`, len(refused), strings.Join(refused, "\n  "))
}
