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
    # What a site that serves something by name gets its certificates with.
    # Named for the one value it holds: the credential for the domain's
    # records.
    length(var.served) == 0 ? {} : {
      certificates = {
        provider  = "cloudflare"
        dns_token = cloudflare_account_token.certificates[0].value
      }
    },
  )
}

output "served" {
  description = "The hostname route for each name this site serves."
  value       = cloudflare_zero_trust_network_hostname_route.served
}
