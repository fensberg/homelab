# The name of each thing a device reaches, resolving to its own address. One
# record for each route of each site, so a name exists exactly where a route
# does and goes when the route does.
#
# Made with the provider it is handed, which is the operator's account and
# not the estate's: this module holds nothing of the estate's.
terraform {
  required_providers {
    cloudflare = { source = "cloudflare/cloudflare" }
  }
}

variable "domain" {
  type        = string
  description = "The domain every name is under."
}

variable "records" {
  type = map(object({
    name    = string
    address = string
    route   = string
  }))
  description = "Each name and the address it resolves to, by site and route."
}

# Looked up by its name: the zone's id is the vendor's, and the name is what
# the vault holds. A domain the operator's token cannot see fails here.
data "cloudflare_zone" "served" {
  filter = {
    name = var.domain
  }
}

# Not proxied: the address is private, and what answers on it is reached
# through the tunnel by an enrolled device and by nothing at the vendor's
# edge.
resource "cloudflare_dns_record" "served" {
  for_each = var.records

  zone_id = data.cloudflare_zone.served.zone_id
  name    = each.value.name
  type    = "A"
  content = each.value.address
  ttl     = 300
  proxied = false
  comment = "what an enrolled device reaches ${each.value.route} by"
}

output "records" {
  description = "Each record made, by site and route."
  value       = cloudflare_dns_record.served
}
