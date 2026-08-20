output "kubeconfig" {
  value     = talos_cluster_kubeconfig.this.kubeconfig_raw
  sensitive = true
}

output "talosconfig" {
  value     = talos_cluster_kubeconfig.this.talos_config
  sensitive = true
}
