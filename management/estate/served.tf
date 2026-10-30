# What a person types to reach something, and what gets it a certificate.
#
# A NAME RESOLVES ONLY FOR AN ENROLLED DEVICE. An enrolled device sends its
# questions to the vendor, and the vendor is given a rule: asked for one of
# a site's names, answer with the address it is reached at. That address is
# the site's own private one, which the device is already sent to through
# the site's tunnel (zero-trust.tf). Anybody else asking is told there is no
# such name, because there is no public record of one. A private name with a
# public record could be pointed elsewhere by whoever changed that record,
# which is why docs/epochs/09-access.md refuses one.
#
# NOT A ROUTE BY NAME THROUGH THE TUNNEL, which is what that record chose
# and what was built first. The route existed, every requirement on the
# vendor's page for one was met, and an enrolled device was still given no
# address (#708). A rule gives up one thing a route has, which nothing here
# uses: telling apart, by name, two things that are not the web and share
# an address. Going back is a change to this file and the one beside the
# tunnel, and to nothing a site runs.
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
  count = var.offline ? 0 : 1

  filter = {
    name = local.domain
  }
}

# Looked up for the reason the bucket permissions are (sites.tf).
data "cloudflare_account_api_token_permission_groups_list" "zone_read" {
  count = var.offline ? 0 : 1

  account_id = local.access.account_id
  name       = "Zone Read"
}

data "cloudflare_account_api_token_permission_groups_list" "dns_write" {
  count = var.offline ? 0 : 1

  account_id = local.access.account_id
  name       = "DNS Write"
}

locals {
  # A rule is consulted in order, and this account holds rules the estate
  # did not make, the first of them at 10000. A site's rules go ahead of
  # every one of those, each site in a hundred of its own, so that no rule
  # made by hand answers for a name before the estate's does.
  rules_from = { for i, k in sort(keys(local.sites)) : k => 1000 + 100 * i }
}

# The vendor carries what an enrolled device sends to a private address
# with this on, which its pages on private networks ask for. Turned on
# 2026-10-12 while a name that would not resolve was being run down. It was
# not the cause of that (#708) and is kept as what the vendor documents.
#
# Account-wide, and so the estate's. It changes what the vendor does with the
# addresses a device already sends through the tunnel and with no others: a
# device sends only what the estate lists (zero-trust.tf).
#
# DECLARED WHOLE. The vendor keeps these settings as one object and refuses
# a write that leaves any of them out. The first write here made the object;
# the next one found three settings the vendor had filled in, planned to
# take them away, and was refused (2026-10-12). So each is written down as
# the account holds it, and a setting the estate has no opinion on is still
# said: an enrolled device may be paused by its user for no time at all,
# is given the vendor's certificate, and has no address of the vendor's own.
resource "cloudflare_zero_trust_device_settings" "estate" {
  account_id                = local.access.account_id
  gateway_proxy_enabled     = true
  gateway_udp_proxy_enabled = true

  disable_for_time                      = 0
  root_certificate_installation_enabled = true
  use_zt_virtual_ip                     = false
}
