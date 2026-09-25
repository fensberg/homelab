variable "config_path" {
  type        = string
  default     = "../../config/estate.rendered.json"
  description = "The estate's rendered config. The lawyer renders it from the estate vault, and no site's build ever reads it."
}

locals {
  config = jsondecode(file(var.config_path))

  # Who may enroll a device and what an enrolled device reaches, and the
  # credential that administers both. Named for the function, not the vendor:
  # access.provider is the one field that says whose it is.
  access = local.config.access

  members = [for m in split(",", local.access.members) : trimspace(m) if trimspace(m) != ""]

  # The estate's name, which leads every bucket it creates, and the sites it
  # grants a plot to. A site's key is positional and in git; its name is a
  # vault value, owned by the estate because the estate names what it grants.
  organization = local.config.organization.name
  sites        = local.config.plots

  # Written once, read by both roots.
  tunnel_routes = jsondecode(file("${path.module}/../tunnel-routes.json")).routes
}
