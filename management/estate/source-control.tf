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

  # Not when planned offline: signing in asks GitHub for a token, and an
  # offline plan holds no credential and asks nothing.
  dynamic "app_auth" {
    for_each = var.offline ? [] : [local.source_control]
    content {
      id              = app_auth.value.app_id
      installation_id = app_auth.value.installation_id
      pem_file        = base64decode(app_auth.value.private_key)
    }
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
  count = var.offline ? 0 : 1

  name = local.source_control_owner
}

# Everybody with a way into the repository that is their own and not a
# team's or the organisation's.
data "github_collaborators" "estate" {
  count = var.offline ? 0 : 1

  owner      = local.source_control_owner
  repository = local.source_control_repository
}

# Every repository of the estate's, each behind the same walls. One, today.
module "repository" {
  source   = "./repository"
  for_each = toset([local.source_control_repository])

  name           = each.key
  default_branch = local.read.default_branch

  pull_request_runners_site = local.source_control.pull_request_runners_site
  delivers_unreviewed       = tonumber(local.source_control.procurement_app_id)
}

data "github_repository" "estate" {
  count = var.offline ? 0 : 1

  full_name = "${local.source_control_owner}/${local.source_control_repository}"
}

# The estate's own runners, and the only work that may reach them: the
# workflows that converge the estate, that converge a site and that test
# against one, as each is on the branch the estate is built from.
#
# TestEveryWorkflowOnTheEstatesRunnersIsOneTheirGroupAdmits holds this list
# and the workflows to each other.
module "runners" {
  source = "./runners"

  name         = "homelab"
  repositories = [local.read.repo_id]
  workflows = [
    for workflow in ["converge-estate.yml", "deploy-infrastructure.yml", "integration-tests.yml"] :
    "${local.source_control_owner}/${local.source_control_repository}/.github/workflows/${workflow}@refs/heads/${local.read.default_branch}"
  ]
}

# The pools a pull request's jobs run in, which cannot be held to workflows
# and are held to this repository.
module "pull_request_runners" {
  source = "./pull-request-runners"

  repositories = [local.read.repo_id]
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
    collaborators                   = local.read.collaborators
    two_factor_is_required          = local.read.two_factor_is_required
    members_may_create_repositories = local.read.members_may_create_repositories
  }

  precondition {
    condition     = local.source_control.provider == "github"
    error_message = "source_control.provider is not 'github', which is what management/estate/ implements."
  }
}
