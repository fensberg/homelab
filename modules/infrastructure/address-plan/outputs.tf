# One object, so the module's callers and `tofu console` read exactly the same
# thing: `jsonencode(local.plan)` is what the contractor asks for.
locals {
  plan = {
    sites = {
      for key in keys(var.sites) : key => merge(local.ranges[key], {
        slug           = local.slugs[key]
        control_planes = local.control_planes[key]
        workers        = local.workers[key]
        dmz_zones      = local.dmz_zones[key]
        dmz            = local.dmz[key]
        hypervisors    = [for h in local.hypervisors[key] : h.hostname]
        fixed_addresses = {
          for name, n in var.fixed_addresses : name => cidrhost(local.ranges[key].service_cidr, n)
        }
      })
    }
    records = merge(local.records, local.alias_records)
  }
}

output "sites" {
  description = "Every site's ranges, machines and slug, by site key."
  value       = local.plan.sites
}

output "records" {
  description = "Every private name and its address, aliases included. Empty until the domain is declared."
  value       = local.plan.records
}

output "unresolved_aliases" {
  description = "Aliases whose target the site does not have, as \"alias -> site\". Empty in any config worth planning; registry.tf refuses one that is not."
  value       = local.unresolved_aliases
}
