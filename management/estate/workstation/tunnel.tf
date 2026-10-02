# The workstation's tunnel: one tunnel, one route, and the token its
# connector runs from. See ../workstation.tf for why it has its own.

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

# The token the connector runs from. It serves this one tunnel and can change
# nothing in the account.
data "cloudflare_zero_trust_tunnel_cloudflared_token" "this" {
  account_id = var.account_id
  tunnel_id  = cloudflare_zero_trust_tunnel_cloudflared.this.id
}
