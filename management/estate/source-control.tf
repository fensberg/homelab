# The estate's own organisation and repository at its source control: who may
# send work to the estate, and what that work may do.
#
# An estate is every repository under one organisation, and the settings of
# that organisation are its outermost wall. They decide who may open a pull
# request, whose workflows run without being let, which runners a workflow
# may use and what its token may do - which is to say, whose code reaches the
# estate's own hardware once a pull request's jobs run there (#559). Every
# one of them was made by hand and read by nothing here.
#
# They are declared and set from here, and set again whenever they are found
# otherwise: a repository's in ./repository, the organisation's here. What
# is not yet declared is read, below, until it is.
#
# THE CREDENTIAL is the estate's alone, as the vendor's token for the
# account is and for the same reason: it can administer the repository and
# the organisation's runners, so whoever held it could take every wall down.
# It is in the estate vault, used by the lawyer, and never granted to a site.
# Kept as the vault keeps a key of more than one line: encoded onto one.
provider "github" {
  owner = local.source_control_owner

  app_auth {
    id              = local.source_control.app_id
    installation_id = local.source_control.installation_id
    pem_file        = base64decode(local.source_control.private_key)
  }
}

locals {
  source_control = local.config.source_control

  # The organisation and the repository, out of the address every site
  # already reconciles from: https://<host>/<organisation>/<repository>.
  source_control_owner      = split("/", trimsuffix(local.source_control.url, ".git"))[3]
  source_control_repository = split("/", trimsuffix(local.source_control.url, ".git"))[4]
}

data "github_organization" "estate" {
  name = local.source_control_owner
}

# Everybody with a way into the repository that is their own and not a
# team's or the organisation's.
data "github_collaborators" "estate" {
  owner      = local.source_control_owner
  repository = local.source_control_repository
}

# Every repository of the estate's, each behind the same walls. One, today.
module "repository" {
  source   = "./repository"
  for_each = toset([local.source_control_repository])

  name = each.key
}

output "source_control" {
  description = "What the estate reads of its own organisation and repository, as counts and switches: nobody's name."
  value = {
    collaborators                   = length(data.github_collaborators.estate.collaborator)
    two_factor_is_required          = data.github_organization.estate.two_factor_requirement_enabled
    members_may_create_repositories = data.github_organization.estate.members_can_create_repositories
  }

  precondition {
    condition     = local.source_control.provider == "github"
    error_message = "source_control.provider is not 'github', which is what management/estate/ implements."
  }
}
