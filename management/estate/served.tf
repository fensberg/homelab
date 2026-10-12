# The name each thing a device reaches is served under.
#
# A certificate is for a name, and a name is under a domain, so every route
# has one: <route>.<domain> while the estate has one site, and
# <route>.<site>.<domain> once it has more, where <site> is what the estate
# calls the site, made a label the way a bucket's name is. The name resolves
# to the route's own private address, which is nowhere from anywhere but an
# enrolled device, so publishing it opens nothing.
#
# WHOSE DOMAIN. The operator's, and not any estate's: whoever can change its
# records can be issued a certificate for a name under it, and being issued
# one is what a subscription is (docs/epochs/11-black-box-white-glove.md).
# It is here, in an estate, as a proof while there is one estate and one
# operator. The real one is kept above every estate.
#
# WHAT IS PUBLISHED. Every certificate is logged in public with its names,
# so a site's label is read by anybody who looks once it has a second site
# beside it. The single-site form carries none.
locals {
  served = local.config.served

  site_label = {
    for k, s in local.sites : k => trim(replace(lower(s.name), "/[^a-z0-9]+/", "-"), "-")
  }
  served_under = {
    for k in keys(local.sites) : k => length(local.sites) == 1 ? local.served.domain : "${local.site_label[k]}.${local.served.domain}"
  }
}

# The operator's own credential, and not the estate's: it changes the
# records of the one domain and nothing else, and the estate's token is not
# given that domain at all. Two providers of one vendor, because they are two
# parties' accounts. This one is here only while the proof is.
provider "cloudflare" {
  alias     = "operator"
  api_token = local.served.api_token
}

# Looked up by its name: the zone's id is the vendor's, and the name is what
# the vault holds. A domain the operator's token cannot see fails here.
data "cloudflare_zone" "served" {
  provider = cloudflare.operator

  filter = {
    name = local.served.domain
  }
}

# The name of each thing a device reaches, resolving to its own address. One
# record for each route of each site, so a name exists exactly where a route
# does and goes when the route does.
#
# Not proxied: the address is private, and what answers on it is reached
# through the tunnel by an enrolled device and by nothing at the vendor's
# edge.
locals {
  served_names = {
    for k in keys(local.sites) : k => { for route, address in local.site_routes[k] : route => "${route}.${local.served_under[k]}" }
  }
  served_records = merge([
    for k in keys(local.sites) : {
      for route, address in local.site_routes[k] : "${k}/${route}" => { name = local.served_names[k][route], address = address, route = route }
    }
  ]...)
}

resource "cloudflare_dns_record" "served" {
  provider = cloudflare.operator
  for_each = local.served_records

  zone_id = data.cloudflare_zone.served.zone_id
  name    = each.value.name
  type    = "A"
  content = each.value.address
  ttl     = 300
  proxied = false
  comment = "what an enrolled device reaches ${each.value.route} by"
}
