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
  # NEITHER OF THESE EDGES MEANS THE API SERVER IS SERVING, and an earlier
  # version of this comment claimed the first one did.
  #
  # talos_cluster_kubeconfig retrieves a credential over the Talos API on port
  # 50000, which answers as soon as the cluster's PKI exists. talos_machine_
  # bootstrap returns when the bootstrap RPC is accepted, not when the control
  # plane has finished coming up. So both can be complete while nothing is
  # listening on 6443 at all - which is what happened: this step ran roughly
  # thirty seconds after bootstrap and got `connection refused`, and with it
  # went every other resource in the wave that touches Kubernetes.
  #
  # The wait is therefore in the script below, where it can be measured, rather
  # than inferred from an edge that does not carry the meaning it looks like it
  # carries.
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

      # Wait for the API server to actually serve.
      #
      # It answers with no node Ready and no CNI installed, because etcd, the
      # apiserver, the controller manager and the scheduler are static pods on
      # host networking - that fact is what makes this whole sequence possible.
      # But "eventually answers" is not "answers now": the static pods still
      # have to be pulled and started, and etcd has to elect. That takes a
      # couple of minutes on a cold cluster and nothing upstream of here waits
      # for it.
      #
      # /readyz is the apiserver's own readiness endpoint, so this asks the
      # server whether it is ready rather than whether a port accepts a
      # connection. Bounded, because a control plane that has not come up in
      # five minutes is broken rather than slow, and the message says which of
      # the two this was.
      attempt=0
      until kubectl get --raw='/readyz' >/dev/null 2>&1; do
        attempt=$((attempt + 1))
        if [ "$attempt" -ge 60 ]; then
          echo "the Kubernetes API server did not become ready within 5 minutes" >&2
          echo "this is upstream of the CNI: nothing has been installed yet" >&2
          exit 1
        fi
        sleep 5
      done

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
