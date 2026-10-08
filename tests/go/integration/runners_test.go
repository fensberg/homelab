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
	opts := k8s.NewKubectlOptions("", kubeconfig(t), "arc-systems")
	pods, err := k8s.ListPodsE(t, opts, metav1.ListOptions{})
	require.NoError(t, err, "listing the runner controller's pods")

	// A listener is named for its set, then a hash, then what it is. The
	// second set's name begins with the first's, so each listener is given
	// to the longest name it starts with.
	listening := map[string]bool{}
	sets := []string{"self-hosted-batch", "self-hosted"}
	for _, pod := range pods {
		if !strings.HasSuffix(pod.Name, "-listener") || pod.Status.Phase != corev1.PodRunning {
			continue
		}
		for _, set := range sets {
			if strings.HasPrefix(pod.Name, set+"-") {
				listening[set] = true
				break
			}
		}
	}
	for _, set := range sets {
		assert.True(t, listening[set],
			"the runner set %s has no listener running, so no job that asks for it will be given a runner. "+
				"Its release is in the arc-runners namespace; the controller's log says why it made no listener", set)
	}
}
