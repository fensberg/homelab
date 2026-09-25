variable "config_path" {
  type        = string
  default     = "../../config/management.rendered.json"
  description = "The rendered config, the same file every site's build reads."
}

locals {
  config = jsondecode(file(var.config_path))

  # The Cloudflare account is the estate. It arrives as
  # object_storage.account_id because object storage was the first thing in
  # it; see the note in management/cluster/tunnel.tf.
  account_id = local.config.object_storage.account_id
  tunnel     = local.config.tunnel

  members = [for m in split(",", local.tunnel.members) : trimspace(m) if trimspace(m) != ""]

  # Written once, read by both roots.
  tunnel_routes = jsondecode(file("${path.module}/../tunnel-routes.json")).routes
}
