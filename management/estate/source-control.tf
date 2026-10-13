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

  name                = each.key
  default_branch      = data.github_repository.estate.default_branch
  delivers_unreviewed = tonumber(local.source_control.procurement_app_id)
}

data "github_repository" "estate" {
  full_name = "${local.source_control_owner}/${local.source_control_repository}"
}

# The estate's own runners, and the only work that may reach them: the
# workflows that converge a site and that test against one, as each is on
# the branch the estate is built from.
#
# TestEveryWorkflowOnTheEstatesRunnersIsOneTheirGroupAdmits holds this list
# and the workflows to each other.
module "runners" {
  source = "./runners"

  name         = "homelab"
  repositories = [data.github_repository.estate.repo_id]
  workflows = [
    for workflow in ["deploy-infrastructure.yml", "integration-tests.yml"] :
    "${local.source_control_owner}/${local.source_control_repository}/.github/workflows/${workflow}@refs/heads/${data.github_repository.estate.default_branch}"
  ]
}

# The organisation's own settings: what a member may do for being one.
#
# A MEMBER IS GIVEN NOTHING. Reading the estate's repositories comes with
# membership; making a repository or a site of pages does not, and every
# repository the estate has is one somebody with the estate's keys made.
#
# Adopted, and never made: the lawyer takes the organisation's settings as
# they stand into the estate's record before anything is applied, because
# this resource writes every field it has and the ones nobody wrote here
# would be written empty. Who the organisation says it is - its name, its
# description, where it is - is left as it was set by hand.
#
# What a new repository starts with is as it stood, and to tighten when
# there is a second repository to start.
resource "github_organization_settings" "estate" {
  billing_email = local.source_control.billing_email

  default_repository_permission           = "read"
  members_can_create_repositories         = false
  members_can_create_public_repositories  = false
  members_can_create_private_repositories = false
  members_can_create_pages                = false
  members_can_create_public_pages         = false
  members_can_create_private_pages        = false
  members_can_fork_private_repositories   = false
  web_commit_signoff_required             = false
  has_organization_projects               = true
  has_repository_projects                 = true

  advanced_security_enabled_for_new_repositories               = false
  dependabot_alerts_enabled_for_new_repositories               = false
  dependabot_security_updates_enabled_for_new_repositories     = false
  dependency_graph_enabled_for_new_repositories                = false
  secret_scanning_enabled_for_new_repositories                 = false
  secret_scanning_push_protection_enabled_for_new_repositories = false

  lifecycle {
    ignore_changes = [name, company, blog, email, description, location, twitter_username]
  }
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
