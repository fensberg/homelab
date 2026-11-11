# What a workflow in the repository may run: the actions the approved
# suppliers list names, GitHub's own included, and nothing else; and each
# only at a full commit, so what runs is what was read and not whatever a tag
# points at today.
#
# An action that is not on the list is refused by the platform before a line
# of it runs, whoever wrote the workflow that asks for it. A new one is added
# to the list first, in a pull request of its own, and
# policy/conftest/rules/supplier_actions fails until it is.
locals {
  actions = yamldecode(file("${path.module}/../../../scripts/approved-suppliers.yml")).actions
}

resource "github_actions_repository_permissions" "this" {
  repository           = var.name
  enabled              = true
  allowed_actions      = "selected"
  sha_pinning_required = true

  allowed_actions_config {
    github_owned_allowed = false
    verified_allowed     = false
    patterns_allowed     = sort([for a in local.actions : "${a.action}@*"])
  }
}

# What the token a workflow is handed may do when the workflow asks for
# nothing: read, and never approve a pull request. A job that needs more
# says so itself, where a reviewer reads it.
resource "github_workflow_repository_permissions" "this" {
  repository                       = var.name
  default_workflow_permissions     = "read"
  can_approve_pull_request_reviews = false
}
