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
