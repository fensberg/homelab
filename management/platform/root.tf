# =============================================================================
# A site's platform root: what is put on a cluster that already stands.
#
# Everything it creates is the module's (modules/infrastructure/platform).
# What is here is what makes this a root: the kubernetes provider, configured
# from the cluster root's outputs handed in as variables (versions.tf), and
# where its state is kept (backend_pg.tf.disabled).
# =============================================================================

variable "site" {
  type        = string
  default     = "site0"
  description = "Which key in the config's sites map this serves. Checked by the module."
}

variable "config_path" {
  type        = string
  default     = "../../config/management.rendered.json"
  description = "Path to the rendered config JSON, read by the module."
}

variable "cluster_access" {
  type = object({
    host               = string
    ca_certificate     = string
    client_certificate = string
    client_key         = string
  })
  sensitive   = true
  description = <<-EOT
    How to reach the cluster's API: the cluster root's output of the same
    name, handed over by the contractor. Structured rather than a kubeconfig
    to parse, so each certificate is a value of its own - which is what lets
    a plan against the as-built record run with stand-ins.
  EOT
}

variable "kubeconfig" {
  type        = string
  sensitive   = true
  description = <<-EOT
    The same access as a kubeconfig, the cluster root's output of the same
    name. Only for kubectl in the Flux bootstrap, which cannot authenticate
    from values held in memory.
  EOT
}

module "platform" {
  source = "../../modules/infrastructure/platform"

  site        = var.site
  config_path = var.config_path
  kubeconfig  = var.kubeconfig
}

output "state_db_endpoint" {
  value = module.platform.state_db_endpoint
}

output "state_conn_str" {
  value     = module.platform.state_conn_str
  sensitive = true
}
