locals {
  kubeconfig = yamldecode(data.talos_cluster_kubeconfig.this.kubeconfig_raw)
}

resource "flux_bootstrap_git" "this" {
  depends_on = [data.talos_cluster_kubeconfig.this]
  path       = local.flux_target_path
}
