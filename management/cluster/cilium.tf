# The pod network.
#
# Vendor: Cilium (chart from helm.cilium.io, images from quay.io), approved in
# scripts/approved-suppliers.yml and pinned by CILIUM_VERSION in
# scripts/versions.env.
#
# This is the one component that cannot arrive the way everything else does.
# Flux reconciles OpenEBS, CloudNativePG and the runner, and it can only do
# that once nodes are Ready - but a node with no CNI never reaches Ready, so
# the CNI has to be in place first. That is why it is here, in the tier that
# builds the cluster, rather than in clusters/management/ with the rest.
#
# The full reasoning, including why Talos inlineManifests and the Helm provider
# were both rejected, is in docs/epochs/03-workload.md.

resource "terraform_data" "cilium" {
  # The kubeconfig is referenced below, which orders this after the API server
  # exists. Bootstrap is named as well because that is the operation this
  # actually waits on, and an implicit edge through a credential is not the
  # place to record a dependency somebody needs to see.
  depends_on = [talos_machine_bootstrap.this]

  # Re-applies whenever the rendered manifest changes, which is the only way it
  # changes: `task render-cni` regenerates it from the pinned chart version.
  # A version bump therefore lands like any other reviewed change to this path.
  triggers_replace = [
    filesha256("${path.module}/../../clusters/bootstrap/cilium.yaml"),
  ]

  provisioner "local-exec" {
    # Same shape as the Flux bootstrap in gitops.tf, and for the same reason:
    # kubectl cannot authenticate from in-memory values the way the kubernetes
    # and talos providers do, and this project's invariant is that nothing
    # survives on the workstation past the run that needed it.
    environment = {
      KUBECONFIG_CONTENT = talos_cluster_kubeconfig.this.kubeconfig_raw
    }
    command = <<-EOT
      set -euo pipefail
      tmp=$(mktemp)
      trap 'rm -f "$tmp"' EXIT
      printf '%s' "$KUBECONFIG_CONTENT" >"$tmp"
      export KUBECONFIG="$tmp"

      # The API server answers here even though no node is Ready: etcd, the
      # apiserver, the controller manager and the scheduler are static pods on
      # host networking, so the control plane comes up with no CNI at all.
      # That fact is what makes this whole sequence possible.
      kubectl apply -f "${path.module}/../../clusters/bootstrap/cilium.yaml"

      # Wait for the agents rather than leaving it to the health gate.
      #
      # Both would catch a broken CNI, but they blame different things. The
      # health gate reports "the cluster is not healthy" ten minutes later,
      # which is true and useless. This reports that Cilium did not roll out,
      # at the step that installed it.
      kubectl -n kube-system rollout status daemonset/cilium --timeout=5m
    EOT
  }
}
