# The workstation's tunnel: one tunnel and one route. See ../workstation.tf
# for why it has its own, and why its token is granted to nobody.

resource "cloudflare_zero_trust_tunnel_cloudflared" "this" {
  account_id = var.account_id
  name       = "${var.estate}-${var.name}"
  config_src = "cloudflare"
}

# No public hostname: the tunnel answers nothing from the internet. It is
# reached only as a private route, by a device already enrolled.
resource "cloudflare_zero_trust_tunnel_cloudflared_config" "this" {
  account_id = var.account_id
  tunnel_id  = cloudflare_zero_trust_tunnel_cloudflared.this.id
  config = {
    ingress = [{ service = "http_status:404" }]
  }
}

# The one route: the workstation itself.
resource "cloudflare_zero_trust_tunnel_cloudflared_route" "this" {
  account_id = var.account_id
  tunnel_id  = cloudflare_zero_trust_tunnel_cloudflared.this.id
  network    = "${var.address}/32"
  comment    = var.name
}
