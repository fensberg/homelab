# The estate's address plan, for every site the config declares. This root
# builds one site and reads that site's slice (local.net in variables.tf); the
# plan is computed for the whole estate because "no two sites share a range"
# is a fact about the estate.
#
# The domain and the aliases name what the plan addresses; the tunnel routes
# are the Services that need an address known before they exist.
module "address_plan" {
  source          = "../address-plan"
  sites           = local.config.sites
  domain          = local.config.organization.domain
  aliases         = local.config.organization.aliases
  fixed_addresses = local.tunnel_routes
}
