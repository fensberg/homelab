# Where a job is handed the estate's own credential, and the only place.
#
# The environment holds one secret, the estate vault's token, which a person
# puts there by hand: a secret set from here would be written into the
# estate's state. What is set from here is who may ask for it - a job on the
# branch the estate is built from, and on no other. A workflow changed on a
# pull request's branch that names this environment is refused it by the
# platform, before it is handed anything.
resource "github_repository_environment" "estate" {
  repository        = var.name
  environment       = "estate"
  can_admins_bypass = false

  deployment_branch_policy {
    protected_branches     = false
    custom_branch_policies = true
  }
}

resource "github_repository_environment_deployment_policy" "estate" {
  repository     = var.name
  environment    = github_repository_environment.estate.environment
  branch_pattern = var.default_branch
}
