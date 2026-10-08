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

# The same, for a key that can list and read a bucket and change nothing in
# it: what the safety officer holds, so the party that checks a copy exists
# can never be what destroys one.
data "cloudflare_account_api_token_permission_groups_list" "r2_bucket_read" {
  account_id = local.access.account_id
  name       = "Workers R2 Storage Bucket Item Read"
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
  r2_bucket_read  = one(data.cloudflare_account_api_token_permission_groups_list.r2_bucket_read.result).id
}

# Read by the lawyer after an apply, and written into each site's -shared
# vault. Sensitive: it is every credential the estate grants.
output "grants" {
  sensitive = true
  value     = { for k, m in module.site : k => merge(m.grants, { heartbeat = module.watchman.heartbeat[k] }) }

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
