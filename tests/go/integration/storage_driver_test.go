//go:build integration

package integration_test

import (
	"testing"

	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The storage driver, asked of the cluster rather than of the manifest.
//
// Its images are pinned through seven value paths, each a repository and a
// tag the chart joins, and a value at a path the chart does not read is
// accepted in silence. And a driver that is running proves nothing about
// whether it can reach the hypervisor: it holds a token confined to two
// paths and verifies the hypervisor against the hypervisor's own authority,
// either of which stops it listing its storage if it is wrong. The capacity
// it publishes is the cheapest evidence that it asked and was answered.
//
// covers: integration:storage-driver

const storageDriverNamespace = "storage-driver"

func TestTheStorageDriverRunsTheImagesItWasPinnedTo(t *testing.T) {
	t.Parallel()
	opts := k8s.NewKubectlOptions("", kubeconfig(t), storageDriverNamespace)

	pods, err := k8s.ListPodsE(t, opts, metav1.ListOptions{})
	require.NoError(t, err, "listing pods in the storage driver's namespace")
	require.NotEmpty(t, pods,
		"no pods in %s: has Flux reconciled the storage driver's HelmRelease, and does the site run a release that writes its Secret?", storageDriverNamespace)

	checked := 0
	for _, pod := range pods {
		assert.Equal(t, corev1.PodRunning, pod.Status.Phase, "pod %s of the storage driver is not running", pod.Name)
		for _, c := range append(append([]corev1.Container{}, pod.Spec.Containers...), pod.Spec.InitContainers...) {
			checked++
			assert.Contains(t, c.Image, "@sha256:",
				"pod %s container %s runs %s, which is a tag rather than a digest. The value that was supposed to pin it is in the driver's HelmRelease, at a path the chart did not read",
				pod.Name, c.Name, c.Image)
		}
	}
	require.NotZero(t, checked, "no containers found, so this asserted nothing")
}

// The driver asked the hypervisor how much room its storage has, and was
// answered. It publishes that as capacity, one object per storage and place,
// so there being any is the driver having authenticated with its confined
// token, verified the hypervisor, and been allowed to look at its own
// storage.
func TestTheStorageDriverHasBeenAnsweredByTheHypervisor(t *testing.T) {
	t.Parallel()
	opts := k8s.NewKubectlOptions("", kubeconfig(t), storageDriverNamespace)

	listed, err := k8s.RunKubectlAndGetOutputE(t, opts, "get", "csistoragecapacities", "-o", "name")
	require.NoError(t, err, "listing the capacity the storage driver publishes")
	assert.NotEmpty(t, listed,
		"the storage driver publishes no capacity, so it has not been answered by the hypervisor about its storage. "+
			"Its controller's log says which of its token, the certificate or the storage's name it was refused over")
}
