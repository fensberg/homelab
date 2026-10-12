# The name of each thing a device reaches in this site, resolving to its own
# address. One record for each route, so a name exists exactly where a route
# does and goes when the route does.
#
# Not proxied: the address is private, and what answers on it is reached
# through the tunnel by an enrolled device and by nothing at the vendor's
# edge.
locals {
  served_names = { for route, address in var.tunnel_routes : route => "${route}.${var.served_under}" }
}

resource "cloudflare_dns_record" "served" {
  for_each = var.tunnel_routes

  zone_id = var.served_zone
  name    = local.served_names[each.key]
  type    = "A"
  content = each.value
  ttl     = 300
  proxied = false
  comment = "what an enrolled device reaches ${each.key} by"
}
