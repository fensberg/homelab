# Every site the estate grants a plot to, one module instance per site key.
# The estate vault lists them (config/estate.tpl.json), and each is the same
# set of objects in management/estate/site.

# Looked up rather than written down: Cloudflare's own docs say a permission's
# name is cosmetic and its id is what a token holds. one() fails the plan if
# the name ever matches nothing or more than one.
data "cloudflare_account_api_token_permission_groups_list" "r2_bucket_write" {
  account_id = local.access.account_id
  name       = "Workers R2 Storage Bucket Item Write"
}

# A name becomes a slug the same way every time: lower case, runs of anything
# but a letter or digit made one hyphen, none at either end.
locals {
  estate_slug = trim(replace(lower(local.organization), "/[^a-z0-9]+/", "-"), "-")
  bucket_prefix = {
    for k, s in local.sites : k => "${local.estate_slug}-${trim(replace(lower(s.name), "/[^a-z0-9]+/", "-"), "-")}"
  }
}

module "site" {
  source   = "./site"
  for_each = local.sites

  account_id      = local.access.account_id
  estate          = local.estate_slug
  prefix          = local.bucket_prefix[each.key]
  site            = each.key
  name            = each.value.name
  tunnel_routes   = local.site_routes[each.key]
  r2_bucket_write = one(data.cloudflare_account_api_token_permission_groups_list.r2_bucket_write.result).id
}

# Read by the lawyer after an apply, and written into each grantee's -shared
# vault: each site's, and the workstation's. Sensitive: it is every credential
# the estate grants.
output "grants" {
  sensitive = true
  value = merge(
    { for k, m in module.site : k => m.grants },
    # The workstation's tunnel, granted as a site's is: the lawyer writes it
    # to <name>-shared, which no site's token can read.
    { for m in module.workstation : local.workstation_grant => m.grants },
  )

  # The workstation's grant is written under its own name, into a vault of
  # that name. A plot keyed the same would have its site's grants replaced.
  precondition {
    condition     = !contains(keys(local.sites), local.workstation_grant)
    error_message = "A plot is keyed \"${local.workstation_grant}\", which is the name the workstation's tunnel is granted under."
  }

  # One address, or none. A range would route a network into a tunnel whose
  # connector may open one port of one machine.
  precondition {
    condition     = local.workstation_address == "" || (can(cidrhost("${local.workstation_address}/32", 0)) && !strcontains(local.workstation_address, "/") && !strcontains(local.workstation_address, ":"))
    error_message = "The workstation's address in the estate's config is not one IPv4 address, so there is nothing to route to it."
  }

  # A plot with no site in the sites' template has no addresses, so its tunnel
  # would route nothing while looking granted.
  precondition {
    condition     = length(local.unplanned_plots) == 0
    error_message = "A plot in the estate vault has no site of that key in config/management.tpl.json, so the address plan has no addresses to route through its tunnel: ${join(", ", local.unplanned_plots)}."
  }

  # R2 names are 3 to 63 characters, and the longest a site gets is its
  # production bucket. Checked here, before anything is created, so an estate
  # or site name too long for it fails the plan rather than half an apply.
  precondition {
    condition = local.estate_slug != "" && alltrue([
      for k, p in local.bucket_prefix : length("${p}-production") <= 63 && !endswith(p, "-")
    ])
    error_message = "A site's bucket names would exceed R2's 63-character limit, or the estate or a site has a name with no letters or digits to name buckets by. Shorten the name in the vault."
  }
}
