//go:build integration

// Package integration_test exercises a real, already-provisioned estate. It
// creates nothing and destroys nothing - everything here is a read against
// infrastructure the start button built, so a failing run is a report about
// the cluster rather than a mess to clean up.
//
//	task render-secrets SITE=site0
//	go test -tags=integration -timeout 20m ./integration/...
//	task clean-secrets SITE=site0
//
// The tier that does create and destroy is e2e/, behind its own build tag and
// its own confirmation.
package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gruntwork-io/terratest/modules/k8s"
	"github.com/gruntwork-io/terratest/modules/retry"
	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"homelab/tests/harness"
)

// kubeconfig pulls the cluster credential out of the OpenTofu state rather
// than expecting one on the runner. It lands in the test's own temp directory,
// which Go removes when the test ends - the same discipline the Sterilize
// phase applies to everything else.
func kubeconfig(t *testing.T) string {
	t.Helper()
	opts := harness.TofuOptions(t, nil)

	raw := terraform.OutputRequired(t, opts, "kubeconfig")
	require.Contains(t, raw, "apiVersion",
		"the kubeconfig output does not look like a kubeconfig; has the Cluster phase run?")

	path := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))
	return path
}

// Every control-plane node answers on the Talos API port. This is the same
// check the Compute phase makes before declaring the VMs up, run against a
// cluster that is supposed to already be healthy.
func TestControlPlaneNodesAnswerTheTalosAPI(t *testing.T) {
	t.Parallel()
	site := harness.SiteConfig(t)

	for i := 0; i < site.ControlPlaneCount; i++ {
		ip := harness.ControlPlaneIP(t, i)
		t.Run(ip, func(t *testing.T) {
			assert.True(t, portOpen(ip, "50000", 10*time.Second),
				"no Talos API on %s:50000. Open that VM's console in Proxmox: no IP on the maintenance banner means the SDN bridge is down, a different IP means cloud-init did not apply the static address.", ip)
		})
	}
}

// etcd quorum is fixed at creation, so a cluster that came up with an even
// number of members stays that way. Worth asserting against the live cluster
// rather than only against the config, because the config is what was asked
// for and this is what was built.
func TestEveryNodeIsReadyAndTheCountIsOdd(t *testing.T) {
	t.Parallel()
	opts := k8s.NewKubectlOptions("", kubeconfig(t), "default")
	site := harness.SiteConfig(t)

	nodes := k8s.GetNodes(t, opts)
	require.Len(t, nodes, site.ControlPlaneCount,
		"the cluster has %d node(s) but the config declares control_plane_count %d", len(nodes), site.ControlPlaneCount)
	assert.Equal(t, 1, len(nodes)%2,
		"an even node count adds an etcd member without adding a tiebreaker; quorum is fixed at creation, so this cannot be fixed by scaling")

	// Polled rather than asserted once: a node can be briefly NotReady
	// during an ordinary Talos or storage-provisioner rollout, and failing on that
	// would make the suite flaky for a reason that is not a defect.
	k8s.WaitUntilAllNodesReady(t, opts, 12, 10*time.Second)
}

// Flux is what makes this cluster self-sustaining, so "Flux reconciled at
// least once and is not failing" is the single most useful integration
// assertion there is: it covers the operator, the manifests, and the
// substitutions from cluster-vars all at once.
func TestFluxKustomizationsAreReconciled(t *testing.T) {
	t.Parallel()
	opts := k8s.NewKubectlOptions("", kubeconfig(t), "flux-system")

	// Polled rather than read once: a reconciliation may be in flight when
	// the test starts, and failing on that would make the suite flaky for a
	// reason that is not a defect.
	retry.DoWithRetry(t, "wait for the Flux Kustomizations to report Ready", 30, 10*time.Second, func() (string, error) {
		out, err := k8s.RunKubectlAndGetOutputE(t, opts,
			"get", "kustomizations.kustomize.toolkit.fluxcd.io", "-A",
			"-o", "jsonpath={range .items[*]}{.metadata.name}={.status.conditions[?(@.type=='Ready')].status}{'\\n'}{end}")
		if err != nil {
			return "", err
		}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line == "" {
				continue
			}
			if !strings.HasSuffix(line, "=True") {
				return "", &notReadyError{line}
			}
		}
		return out, nil
	})
}

type notReadyError struct{ line string }

func (e *notReadyError) Error() string {
	return "a Kustomization is not Ready: " + e.line + " (run `flux get kustomizations -A` for the reason)"
}
