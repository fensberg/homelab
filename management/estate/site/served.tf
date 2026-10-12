# What this site serves by name: a route for each name through the site's
# tunnel, and the credential its certificates are got with. Why each is as it
# is, is in ../served.tf.
resource "cloudflare_zero_trust_network_hostname_route" "served" {
  for_each = var.served

  account_id = var.account_id
  tunnel_id  = cloudflare_zero_trust_tunnel_cloudflared.this.id
  hostname   = each.value.hostname
  comment    = "${each.key} (${var.site})"
}

# None, for a site that serves nothing by name: a credential that changes a
# domain's records is not made in case it is wanted.
resource "cloudflare_account_token" "certificates" {
  count      = length(var.served) > 0 ? 1 : 0
  account_id = var.account_id
  name       = "${var.site} certificates"

  policies = [{
    effect            = "allow"
    permission_groups = [{ id = var.zone_read }, { id = var.dns_write }]
    resources = jsonencode({
      "com.cloudflare.api.account.zone.${var.zone}" = "*"
    })
  }]
}
