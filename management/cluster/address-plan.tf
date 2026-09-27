# The estate's address plan, for every site the config declares. This root
# builds one site and reads that site's slice (local.net in variables.tf); the
# plan is computed for the whole estate because "no two sites share a range"
# is a fact about the estate.
module "address_plan" {
  source = "../../modules/infrastructure/address-plan"
  sites  = local.config.sites
}
