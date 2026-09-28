# Every name the estate resolves privately: <thing>.<site>.<domain>, and the
# declared aliases at <alias>.<domain>. Omitted, not invented, until the
# domain is declared.
#
# These are records for whatever serves them - Cloudflare Tunnel hostname
# routing, per epoch 09 - and never public DNS. An internal name that shadowed
# a public one would be a way to hijack it, which is why aliases are declared
# rather than derived.
locals {
  records = var.domain == "" ? {} : merge([
    for key in keys(var.sites) : merge(
      { for k, m in local.control_planes[key] : "cp-${k}.${key}.${var.domain}" => m.ip },
      { for k, m in local.workers[key] : "wk-${k}.${key}.${var.domain}" => m.ip },
      { for k, m in local.dmz[key] : "${k}.${key}.${var.domain}" => m.ip },
      length(local.hypervisors[key]) > 0 ? { "proxmox.${key}.${var.domain}" = local.hypervisors[key][0].ip } : {},
    )
  ]...)

  # An alias whose target the site does not have is left out and reported,
  # not failed here: whether a config is acceptable is decided at the edges
  # (registry.tf), which refuses a plan with any.
  alias_records = var.domain == "" ? {} : {
    for alias, site in var.aliases : "${alias}.${var.domain}" => local.records["${alias}.${site}.${var.domain}"]
    if contains(keys(local.records), "${alias}.${site}.${var.domain}")
  }
  unresolved_aliases = var.domain == "" ? [] : sort([
    for alias, site in var.aliases : "${alias} -> ${site}"
    if !contains(keys(local.records), "${alias}.${site}.${var.domain}")
  ])
}
