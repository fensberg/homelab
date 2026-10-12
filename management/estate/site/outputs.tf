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
    # The names this site serves under, as one field: what its certificate is
    # issued for, and what the site reads to know what it is called. A site
    # with no route serves under none and is granted none.
    length(local.served_names) == 0 ? {} : {
      served = { names = join(" ", sort(values(local.served_names))) }
    },
  )
}

output "served" {
  description = "The record for each thing a device reaches in this site, by route."
  value       = cloudflare_dns_record.served
}
