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
# itself and whose one route is an address of the workstation's own, held on
# its loopback rather than handed out by the house's router. An enrolled
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
  workstation_name    = "workstation"
}

# One, or none: by number, so that no name is an address in a plan.
module "workstation" {
  source = "./workstation"
  count  = local.workstation_address == "" ? 0 : 1

  account_id = local.access.account_id
  estate     = local.estate_slug
  name       = local.workstation_name
  address    = local.workstation_address
}

# The address an enrolled device reaches the workstation at, or nothing.
#
# The tunnel's token is granted to nobody. The workstation's install pulls it
# from the account when it installs, with the token the estate is converged
# with, so no vault and no service account gains any reach for it
# (workstation/tunnel.sh).
output "workstation_route" {
  value = [for m in module.workstation : m.route]

  # One address, or none. A range would route a network into a tunnel whose
  # connector may open one port of one machine.
  precondition {
    condition     = local.workstation_address == "" || (can(cidrhost("${local.workstation_address}/32", 0)) && !strcontains(local.workstation_address, "/") && !strcontains(local.workstation_address, ":"))
    error_message = "The workstation's address in the estate's config is not one IPv4 address, so there is nothing to route to it."
  }
}
