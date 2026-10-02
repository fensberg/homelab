# What the workstation is granted: the token its connector runs from, and
# nothing else of the estate's.
output "grants" {
  sensitive = true
  value = {
    tunnel = {
      provider = "cloudflare"
      token    = data.cloudflare_zero_trust_tunnel_cloudflared_token.this.token
    }
  }
}

output "route" {
  value = cloudflare_zero_trust_tunnel_cloudflared_route.this.network
}

output "name" {
  value = cloudflare_zero_trust_tunnel_cloudflared.this.name
}
