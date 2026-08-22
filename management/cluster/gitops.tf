# =============================================================================
# GitOps controller. Vendor: Flux (fluxcd/flux provider).
#
# Bootstrapping Flux commits its own manifests to the source-control repository
# under gitops_target_path, then reconciles everything else from there - which
# is how the CloudNativePG operator and the state database arrive.
# =============================================================================

resource "flux_bootstrap_git" "this" {
  depends_on = [talos_cluster_kubeconfig.this]
  path       = local.gitops_target_path
}
