locals {
  kubeconfig = yamldecode(talos_cluster_kubeconfig.this.kubeconfig_raw)
}

resource "flux_bootstrap_git" "this" {
  depends_on = [talos_cluster_kubeconfig.this]
  path       = local.flux_target_path
}
