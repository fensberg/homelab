# Who may enroll a device, and what an enrolled device reaches. All three are
# the account's: Cloudflare keeps one enrollment application and one device
# profile per organisation, and the organisation is the estate.

# Who may enroll a device. The list is personal data, so it lives in the vault.
resource "cloudflare_zero_trust_access_policy" "members" {
  account_id = local.access.account_id
  name       = "estate members"
  decision   = "allow"

  include = [for m in local.members : { email = { email = m } }]

  lifecycle {
    create_before_destroy = true

    precondition {
      condition     = length(local.members) > 0
      error_message = "members is empty, so this policy would admit nobody and no device could enroll. List the members' emails, comma-separated, in op://estate/access/members."
    }
  }
}

# Device enrollment is an Access application of type `warp`: enrolling a WARP
# client is a login to it, and the policy above decides who succeeds.
#
# Cloudflare creates it with the Zero Trust organisation and allows only one,
# so creating it while one exists fails with `application_already_exists`
# (#453). build-estate adopts the one that exists before this is applied and
# creates it only when there is none - in Go, because an import block or a
# data source fails outright when its target is missing (run.AdoptIfOrphaned).
resource "cloudflare_zero_trust_access_application" "enrollment" {
  account_id       = local.access.account_id
  name             = "Warp Login App"
  type             = "warp"
  session_duration = "24h"

  # app_launcher_visible used to be declared here, because provider v4's plan
  # wanted to change it on every run (#474). v5 refuses it on a warp
  # application outright, so a converge showing a change here again is the
  # first thing to look at.
  policies = [{ id = cloudflare_zero_trust_access_policy.members.id, precedence = 1 }]
}

# WARP sends ONLY these addresses through Cloudflare. Everything else a device
# does goes out its own connection as it would without WARP.
#
# An include list rather than the default exclude list: the default excludes
# every private range - 10.0.0.0/8 among them - so the routes would never enter
# the tunnel without editing that list, and an include list says exactly what
# an enrolled device is given. Provider v5 carries the split tunnel on the
# account's default device profile, and setting `include` is what makes it an
# include list.
#
# One list for the whole account, which is why it is here: two sites each
# writing it from their own builds would overwrite each other's routes.
resource "cloudflare_zero_trust_device_default_profile" "estate" {
  account_id = local.access.account_id
  include = [for name, addr in local.tunnel_routes : {
    address     = "${addr}/32"
    description = name
  }]
}
