# What the lawyer writes into the site's -shared vault, item by item. Each key
# is an item and each inner key a field, so this is the whole of what a site is
# granted and the one place it is decided.
output "grants" {
  sensitive = true
  value = merge({
    identity = { name = var.name }

    object_storage = merge(
      { provider = "cloudflare", account_id = var.account_id },
      [for p in local.purposes : {
        "${p}_bucket"                   = cloudflare_r2_bucket.this[p].name
        "${p}_access_key_id"            = cloudflare_account_token.this[p].id
        "${p}_secret_access_key"        = sha256(cloudflare_account_token.this[p].value)
        "${p}_reader_access_key_id"     = cloudflare_account_token.reader[p].id
        "${p}_reader_secret_access_key" = sha256(cloudflare_account_token.reader[p].value)
      }]...
    )

    tunnel = {
      provider = "cloudflare"
      token    = data.cloudflare_zero_trust_tunnel_cloudflared_token.this.token
    }
    },
    # What of this site a device reaches, for the party that names it: each
    # route and its address, and the label that sets this site's names apart
    # when the estate has more than one. The estate does not know the domain
    # a name is under, and makes no name. A site with no route is granted
    # nothing here.
    length(var.tunnel_routes) == 0 ? {} : {
      served = merge(
        { routes = join(" ", [for route in sort(keys(var.tunnel_routes)) : "${route}=${var.tunnel_routes[route]}"]) },
        var.served_under == "" ? {} : { under = var.served_under },
      )
    },
  )
}
