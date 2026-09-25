variable "config_path" {
  type        = string
  default     = "../../config/estate.rendered.json"
  description = "The estate's rendered config. The lawyer renders it from the estate vault, and no site's build ever reads it."
}

locals {
  config = jsondecode(file(var.config_path))

  # The Cloudflare account is the estate.
  account = local.config.account

  members = [for m in split(",", local.config.members) : trimspace(m) if trimspace(m) != ""]

  # Written once, read by both roots.
  tunnel_routes = jsondecode(file("${path.module}/../tunnel-routes.json")).routes
}
