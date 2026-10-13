# What a workflow in the repository may run. As it stands: any action, by
# anybody, at any reference.
#
# TO TIGHTEN: only the owners the workflows already use, and only at a full
# commit - which is how every one of them is already written.
resource "github_actions_repository_permissions" "this" {
  repository           = var.name
  enabled              = true
  allowed_actions      = "all"
  sha_pinning_required = false
}

# What the token a workflow is handed may do when the workflow asks for
# nothing: read, and never approve a pull request. A job that needs more
# says so itself, where a reviewer reads it.
resource "github_workflow_repository_permissions" "this" {
  repository                       = var.name
  default_workflow_permissions     = "read"
  can_approve_pull_request_reviews = false
}
