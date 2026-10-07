//go:build integration

package integration_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/random"
	"github.com/gruntwork-io/terratest/modules/retry"
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

// A claim on the class that outlives a machine is given a volume, the volume
// is attached to a worker, and it is gone again when it is no longer wanted.
//
// The driver running, and even the hypervisor answering it, says nothing
// about the one thing it is for. Making a volume, finding the machine the
// pod landed on and attaching the volume to it each use the driver's token
// differently, and that token is confined to two paths on the hypervisor.
// Nothing short of a claim exercises all three - so this makes one.
//
// THE ONE TEST IN THIS TIER THAT WRITES. Everything else here reads an
// estate that is already built. This makes a namespace, a one-gibibyte
// claim and a pod that does nothing, and removes all three and the volume
// behind them, whether it passes or not. It is here and not in a tier of
// its own because what it proves is a property of the built estate, asked
// nightly, and because the alternative was a script somebody runs once.
func TestAClaimOnTheKeptClassIsAttachedToAWorkerAndRemoved(t *testing.T) {
	namespace := "storage-driver-trial-" + strings.ToLower(random.UniqueId())
	opts := k8s.NewKubectlOptions("", kubeconfig(t), namespace)
	k8s.CreateNamespace(t, opts, namespace)

	// The class keeps a volume when its claim is deleted, which is right for
	// data and wrong for a trial: the volume would stay on the hypervisor
	// with nothing pointing at it. So the trial's own volume is told to go
	// with its claim, and the claim, the pod and the namespace go after.
	t.Cleanup(func() {
		volume, _ := k8s.RunKubectlAndGetOutputE(t, opts, "get", "pvc", "trial", "-o", "jsonpath={.spec.volumeName}")
		if volume != "" {
			_, _ = k8s.RunKubectlAndGetOutputE(t, opts, "patch", "pv", volume, "-p", `{"spec":{"persistentVolumeReclaimPolicy":"Delete"}}`)
		}
		_ = k8s.RunKubectlE(t, opts, "delete", "pod", "trial", "--ignore-not-found", "--wait=true", "--timeout=120s")
		_ = k8s.RunKubectlE(t, opts, "delete", "pvc", "trial", "--ignore-not-found", "--wait=true", "--timeout=120s")
		if volume != "" {
			_, err := retry.DoWithRetryE(t, "the trial's volume is removed from the hypervisor", 24, 5*time.Second, func() (string, error) {
				out, err := k8s.RunKubectlAndGetOutputE(t, opts, "get", "pv", volume, "--ignore-not-found", "-o", "name")
				if err != nil || out != "" {
					return "", fmt.Errorf("the volume %s is still there", volume)
				}
				return "", nil
			})
			assert.NoError(t, err, "the trial's volume was not removed, so a disk nothing claims is left in the driver's storage on the hypervisor")
		}
		k8s.DeleteNamespace(t, opts, namespace)
	})

	k8s.KubectlApplyFromString(t, opts, trialClaim)
	err := k8s.WaitUntilPodAvailableE(t, opts, "trial", 36, 5*time.Second)
	if err != nil {
		events, _ := k8s.RunKubectlAndGetOutputE(t, opts, "get", "events", "--sort-by=.lastTimestamp")
		require.NoError(t, err, "a pod with a claim on the class did not start, so the driver did not make its volume or did not attach it.\n\n%s", events)
	}

	bound, err := k8s.RunKubectlAndGetOutputE(t, opts, "get", "pvc", "trial", "-o", "jsonpath={.status.phase}")
	require.NoError(t, err)
	assert.Equal(t, "Bound", bound, "the pod started and its claim is not bound")

	// Asked through the client and not through kubectl, whose output this
	// harness logs: a node's name carries the site's, which is a vault value,
	// and this tier's log is public.
	pod, err := k8s.GetPodE(t, opts, "trial")
	require.NoError(t, err)
	onAControlPlane := false
	for _, node := range k8s.GetNodes(t, opts) {
		if node.Name == pod.Spec.NodeName {
			_, onAControlPlane = node.Labels["node-role.kubernetes.io/control-plane"]
		}
	}
	assert.False(t, onAControlPlane, "the pod with the claim runs on a control plane, where the driver's token may attach nothing")
}

// A claim on the class, and a pod that holds it and does nothing else. Off
// the control planes, because the driver may attach a volume only to a
// machine in the worker pool; and held to the restricted profile, so the
// trial asks the cluster for nothing a real workload would not get.
const trialClaim = `
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: trial
spec:
  storageClassName: outlives-a-machine
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: 1Gi
---
apiVersion: v1
kind: Pod
metadata:
  name: trial
spec:
  affinity:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
          - matchExpressions:
              - key: node-role.kubernetes.io/control-plane
                operator: DoesNotExist
  securityContext:
    runAsNonRoot: true
    runAsUser: 65532
    fsGroup: 65532
    seccompProfile:
      type: RuntimeDefault
  containers:
    - name: hold
      image: registry.k8s.io/pause:3.10@sha256:ee6521f290b2168b6e0935a181d4cff9be1ac3f505666ef0e3c98fae8199917a
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        capabilities:
          drop: [ALL]
      resources:
        requests:
          cpu: 10m
          memory: 16Mi
      volumeMounts:
        - name: data
          mountPath: /data
  volumes:
    - name: data
      persistentVolumeClaim:
        claimName: trial
`
