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

// Each set of runners is listening for work.
//
// The estate's runners are two sets, split by the priority of their work: one
// for work that cannot wait, which is a converge, and one for work that can.
// A set makes no runner until a job asks for one, so between jobs the only
// sign that a set exists at all is its listener - the small pod that holds
// the connection to GitHub and asks for a runner when a job arrives. A set
// whose listener is not running takes no work, and a job that asks for it
// waits for a day and then fails, having said nothing.
//
// covers: integration:self-hosted
// covers: integration:self-hosted-batch
func TestEverySetOfRunnersIsListeningForWork(t *testing.T) {
	t.Parallel()
	const runners, controller = "arc-runners", "arc-systems"

	// A release is named for the estate and its set for the site, so the
	// cluster is asked which set each release made. Helm writes the release
	// on everything it installs.
	made, err := k8s.RunKubectlAndGetOutputE(t, k8s.NewKubectlOptions("", kubeconfig(t), runners),
		"get", "autoscalingrunnersets", "-o",
		`jsonpath={range .items[*]}{.metadata.annotations.meta\.helm\.sh/release-name}={.metadata.name}{"\n"}{end}`)
	require.NoError(t, err, "listing the sets of runners the releases made")
	setOf := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(made), "\n") {
		if release, set, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			setOf[release] = set
		}
	}

	// A listener runs beside the controller and is labelled with the set it
	// listens for.
	pods, err := k8s.ListPodsE(t, k8s.NewKubectlOptions("", kubeconfig(t), controller),
		metav1.ListOptions{LabelSelector: "actions.github.com/scale-set-namespace=" + runners})
	require.NoError(t, err, "listing the listeners beside the runner controller")
	listening := map[string]bool{}
	for _, pod := range pods {
		if pod.Status.Phase == corev1.PodRunning {
			listening[pod.Labels["actions.github.com/scale-set-name"]] = true
		}
	}

	for _, release := range []string{"self-hosted", "self-hosted-batch"} {
		set, ok := setOf[release]
		if !assert.True(t, ok,
			"the release %s has made no set of runners in the %s namespace, so there is nothing for a job to ask for. "+
				"The release's own status says why", release, runners) {
			continue
		}
		assert.True(t, listening[set],
			"the set of runners the release %s made has no listener running in the %s namespace, so no job that asks "+
				"for it will be given a runner. The controller's log says why it made no listener", release, controller)
	}
}
