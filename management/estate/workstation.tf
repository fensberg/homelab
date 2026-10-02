# =============================================================================
# The workstation's own tunnel.
#
# The workstation is the machine the estate is built from. It is not part of
# any site and no site's lifecycle can touch it (workstation/provision.yml),
# so it is not reached through a site's tunnel either: a site's connector
# runs in that site's cluster, and the day the cluster is broken is the day
# the operator most needs to reach the machine that repairs it.
#
# So it has a tunnel of its own, whose connector runs on the workstation
# itself and whose one route is the workstation's own address. An enrolled
# device sends that address to Cloudflare, exactly as it sends a site's
# routes, and reaches the workstation's SSH through it. Nothing listens on
# the house's public address, and the operator's laptop joins no overlay.
#
# What the connector may open is decided on the workstation and not here
# (workstation/tunnel.sh): its own SSH port, and nothing else in the house.
# A route added to this tunnel by mistake, or by somebody who took the
# account, still reaches nothing but that.
#
# Optional. An estate whose config gives the workstation no address has no
# such tunnel.
# =============================================================================

locals {
  workstation_address = trimspace(try(local.config.workstation.address, ""))

  # Who the token is granted to: the lawyer writes it to the vault
  # <grant>-shared, as it writes each site's to <site>-shared. Not a site's
  # key, and a plot of this name is refused below.
  workstation_grant = "workstation"
}

# One, or none: by number, so that no name is an address in a plan.
module "workstation" {
  source = "./workstation"
  count  = local.workstation_address == "" ? 0 : 1

  account_id = local.access.account_id
  estate     = local.estate_slug
  name       = local.workstation_grant
  address    = local.workstation_address
}
