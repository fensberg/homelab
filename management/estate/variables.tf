variable "sites_path" {
  type        = string
  default     = "../../config/management.tpl.json"
  description = <<-EOT
    The sites' config template, read for what the address plan computes from:
    each site's octet and machines. Only committed values are read - never a
    rendered file - so the estate learns each site's addresses without holding
    anything a site's vault holds.
  EOT
}

variable "applications_path" {
  type        = string
  default     = null
  description = <<-EOT
    Where the applications are, each declaring the routes an enrolled device
    is given to it. Unset outside tests: the estate reads the repository's own.
  EOT
}

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

  # Each site's routes as addresses, by the site key the estate grants a plot
  # to. A plot the sites' template does not declare has none, and the grants
  # output refuses it rather than leaving its tunnel routing nothing.
  site_routes = {
    for k in keys(local.sites) : k => try(module.address_plan.sites[k].fixed_addresses, {})
  }
  unplanned_plots = [for k in keys(local.sites) : k if !contains(keys(module.address_plan.sites), k)]
}

# The estate's address plan, read for the tunnel routes: each site routes its
# own addresses, and the split tunnel carries every site's (#536).
module "address_plan" {
  source          = "../../modules/infrastructure/address-plan"
  sites           = jsondecode(file(var.sites_path)).sites
  fixed_addresses = module.applications.routes
}

# What each application declares about itself. The routes are a host number
# in every site's service range, which the address plan turns into each
# site's own address.
module "applications" {
  source    = "../../modules/infrastructure/applications"
  directory = var.applications_path
}
