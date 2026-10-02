output "route" {
  value = cloudflare_zero_trust_tunnel_cloudflared_route.this.network
}

output "name" {
  value = cloudflare_zero_trust_tunnel_cloudflared.this.name
}
