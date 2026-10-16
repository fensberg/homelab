# What a workflow in the repository may run: the platform's own actions, the
# few others written here, and nothing else; and each only at a full commit,
# so what runs is what was read and not whatever a tag points at today.
#
# An action that is not here is refused by the platform before a line of it
# runs, whoever wrote the workflow that asks for it. A new one is added
# here first, by the estate, and
# policy/conftest/allowed_actions.rego fails until it is.
resource "github_actions_repository_permissions" "this" {
  repository           = var.name
  enabled              = true
  allowed_actions      = "selected"
  sha_pinning_required = true

  allowed_actions_config {
    github_owned_allowed = true
    verified_allowed     = false
    patterns_allowed = [
      "aquasecurity/trivy-action@*",
      # Called by the one above, not by a workflow: the platform holds an
      # action that an action calls to the same list.
      "aquasecurity/setup-trivy@*",
      "ossf/scorecard-action@*",
      "pnpm/action-setup@*",
      "step-security/harden-runner@*",
    ]
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
