output "kubeconfig" {
  value     = talos_cluster_kubeconfig.this.kubeconfig_raw
  sensitive = true
}

output "talosconfig" {
  value     = data.talos_client_configuration.this.talos_config
  sensitive = true
}

# What management/platform/ configures its kubernetes provider from. The
# provider's structured form rather than the kubeconfig to parse: each
# certificate is a value of its own, so a plan against the as-built record
# gets a stand-in of the right shape for each.
output "cluster_access" {
  value     = talos_cluster_kubeconfig.this.kubernetes_client_configuration
  sensitive = true
}
