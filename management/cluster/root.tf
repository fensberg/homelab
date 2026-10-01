# =============================================================================
# A site's cluster root: the machines, Talos and the cluster's network, ending
# when every node is Ready.
#
# Everything it builds is the module's (modules/infrastructure/cluster). What
# is here is what makes this a root rather than a module: which providers, at
# which versions, configured from which credentials (versions.tf), and where
# its state is kept (backend_pg.tf.disabled). A second site is this root again
# with another site selected, so nothing a site builds is written twice.
# =============================================================================

variable "site" {
  type        = string
  default     = "site0"
  description = "Which key in the config's sites map to build. Checked by the module."
}

variable "config_path" {
  type        = string
  default     = "../../config/management.rendered.json"
  description = "Path to the rendered config JSON, read by the module and by the provider blocks."
}

variable "offline" {
  type        = bool
  default     = false
  description = "Whether this plan must reach nothing. See the module."
}

variable "overlay_key_wanted" {
  type        = bool
  default     = false
  description = "Whether the hypervisor's overlay key should exist. See the module."
}

# Only what the provider blocks need. Everything else the config holds is
# read by the module.
locals {
  config          = jsondecode(file(var.config_path))
  site            = local.config.sites[var.site]
  hypervisors     = [for k in sort(keys(local.site.hypervisor.nodes)) : local.site.hypervisor.nodes[k]]
  overlay_network = local.site.overlay_network
}

module "cluster" {
  source = "../../modules/infrastructure/cluster"

  site               = var.site
  config_path        = var.config_path
  offline            = var.offline
  overlay_key_wanted = var.overlay_key_wanted
}

# The module's outputs, under the names the contractor and the platform root
# read them by.
output "kubeconfig" {
  value     = module.cluster.kubeconfig
  sensitive = true
}

output "talosconfig" {
  value     = module.cluster.talosconfig
  sensitive = true
}

output "cluster_access" {
  value     = module.cluster.cluster_access
  sensitive = true
}

output "overlay_network_auth_key" {
  value     = module.cluster.overlay_network_auth_key
  sensitive = true
}

output "overlay_router_tag" {
  value = module.cluster.overlay_router_tag
}

output "site_network" {
  value = module.cluster.site_network
}
