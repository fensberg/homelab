# What a person types to reach something, and what gets it a certificate.
#
# A NAME RESOLVES ONLY FOR AN ENROLLED DEVICE. Each name is a hostname route
# on its site's tunnel and has no public record: the vendor answers an
# enrolled device's question with an address of its own, carries what is
# sent there into the tunnel, and the site's connector takes it to the name's
# real address. Anybody else asking is told there is no such name. A private
# name with a public record could be pointed elsewhere by whoever changed
# that record, which is why docs/epochs/09-access.md refuses one.
#
# A CERTIFICATE IS GOT BY THE SITE, FOR ITSELF. The issuer wants proof that
# a name is the estate's, and the proof is a record under the estate's
# domain. So each site that serves a name is granted a credential that can
# change that domain's records, made here and narrowed as far as the vendor
# narrows one - to the one domain, which is every record under it and not
# only the proofs. Nothing of the estate's own token reaches a site.

# The domain, looked up by its name: the zone's id is the vendor's, and the
# name is what the vault holds. A domain the estate's token cannot see fails
# here.
data "cloudflare_zone" "estate" {
  filter = {
    name = local.domain
  }
}

# Looked up for the reason the bucket permissions are (sites.tf).
data "cloudflare_account_api_token_permission_groups_list" "zone_read" {
  account_id = local.access.account_id
  name       = "Zone Read"
}

data "cloudflare_account_api_token_permission_groups_list" "dns_write" {
  account_id = local.access.account_id
  name       = "DNS Write"
}

variable "resolved_through" {
  type        = list(string)
  default     = ["172.64.128.0/20", "2606:4700:cf1:4000::/64"]
  description = <<-EOT
    The addresses the vendor answers an enrolled device with when it asks for
    a private name, from the vendor's own page on private hostnames, read
    2026-10-12. A device sends only what the estate lists through the tunnel,
    so these are listed, or a name would resolve and lead nowhere.
  EOT
}

locals {
  # Only when something is served by a name: an estate with none sends
  # nothing more through the vendor than it did.
  names_are_served = anytrue([for served in values(local.site_served) : length(served) > 0])
}
