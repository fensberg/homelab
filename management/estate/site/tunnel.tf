# The site's tunnel: how somebody off the LAN reaches a service in this site
# without a port forward. cloudflared runs in the site's cluster and dials out;
# an enrolled WARP device is routed to the addresses below through it.
#
# Created by the estate because creating a tunnel needs a token that can create
# and delete every tunnel in the account. The site is granted only the run
# token, which serves this one tunnel and can change nothing.
resource "cloudflare_zero_trust_tunnel_cloudflared" "this" {
  account_id = var.account_id
  name       = "${var.estate}-${var.site}"
  config_src = "cloudflare"
}

# Private routing only: no public hostname, and a 404 for anything else.
resource "cloudflare_zero_trust_tunnel_cloudflared_config" "this" {
  account_id = var.account_id
  tunnel_id  = cloudflare_zero_trust_tunnel_cloudflared.this.id
  config = {
    ingress = [{ service = "http_status:404" }]
  }
}

resource "cloudflare_zero_trust_tunnel_cloudflared_route" "this" {
  for_each   = var.tunnel_routes
  account_id = var.account_id
  tunnel_id  = cloudflare_zero_trust_tunnel_cloudflared.this.id
  network    = "${each.value}/32"
  comment    = each.key
}

data "cloudflare_zero_trust_tunnel_cloudflared_token" "this" {
  account_id = var.account_id
  tunnel_id  = cloudflare_zero_trust_tunnel_cloudflared.this.id
}
