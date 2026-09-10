package repo

import (
	"regexp"
	"strings"
	"testing"
)

// Nothing may talk to Kubernetes before the cluster is known to be up.
//
// WHAT THIS GUARDS, and it is not style. OpenTofu runs everything it can in
// parallel, and a kubernetes_* resource with no edge into the cluster's own
// readiness starts in the very first wave - while the API server is still
// pulling its static pods. The provider gets `connection refused` and the whole
// apply dies, which on an ignition means the run tears the estate back down.
//
// That happened. `kubernetes_namespace.valheim` and
// `kubernetes_secret.runner_vars` both went that way in one run, thirty seconds
// after bootstrap. Neither had a bug in it; both were simply unordered, and
// their siblings two lines away were not.
//
// The gate is data.talos_cluster_health.this, which is the estate's answer to
// "is this cluster real yet". A resource satisfies this either by naming it in
// depends_on, or by referencing a kubernetes_namespace that does - a secret
// that reads its namespace off a gated namespace resource inherits the edge,
// which is the ordinary and preferable spelling.
//
// A namespace, having nothing above it to inherit from, must always say so
// itself. The literal-string spelling is the trap: `namespace = "flux-system"`
// looks like a reference and carries no dependency whatsoever.
func TestEveryKubernetesResourceWaitsForTheCluster(t *testing.T) {
	const gate = "data.talos_cluster_health.this"

	files := []string{
		"management/cluster/gitops.tf",
		"management/cluster/database.tf",
		"management/cluster/runner.tf",
		"management/cluster/workloads.tf",
	}

	block := regexp.MustCompile(`(?s)resource\s+"(kubernetes_[A-Za-z0-9_]+)"\s+"([A-Za-z0-9_]+)"\s*\{(.*?)\n\}`)
	namespaceRef := regexp.MustCompile(`kubernetes_namespace\.[A-Za-z0-9_]+`)

	found := 0
	for _, f := range files {
		body := readRepoFile(t, f)
		for _, m := range block.FindAllStringSubmatch(body, -1) {
			kind, name, inner := m[1], m[2], m[3]
			found++

			gated := strings.Contains(inner, gate)
			if kind == "kubernetes_namespace" {
				if !gated {
					t.Errorf(`%s.%s in %s does not depend on %s.

A namespace has nothing above it to inherit an ordering from, so it must name
the gate itself:

    depends_on = [%s]

Without it this is created in the first wave of the apply, against an API
server that has not finished starting, and the run fails with connection
refused.`, kind, name, f, gate, gate)
				}
				continue
			}

			if !gated && !namespaceRef.MatchString(inner) {
				t.Errorf(`%s.%s in %s has no edge to the cluster being ready.

It neither names %s in depends_on nor takes its namespace from a
kubernetes_namespace resource. Writing the namespace as a literal string looks
like a reference and creates no dependency at all, so this starts before the
API server is serving.`, kind, name, f, gate)
			}
		}
	}

	if found == 0 {
		t.Fatal("no kubernetes_* resources found - this guard is looking at the wrong files")
	}
}
