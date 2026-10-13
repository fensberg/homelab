output "walls" {
  description = "What is set on the repository, as switches: for the root's tests and its read-out."
  value = {
    repository               = github_workflow_repository_permissions.this.repository
    a_workflows_token        = github_workflow_repository_permissions.this.default_workflow_permissions
    a_workflow_may_approve   = github_workflow_repository_permissions.this.can_approve_pull_request_reviews
    actions_run              = github_actions_repository_permissions.this.allowed_actions
    actions_at_a_commit_only = github_actions_repository_permissions.this.sha_pinning_required
    actions_set_on           = github_actions_repository_permissions.this.repository
  }
}

output "rules" {
  description = "Who may pass each rule on the repository's branches and tags by, as a count per rule: for the root's tests."
  value = {
    for name, rule in {
      releases       = github_repository_ruleset.releases
      all_branches   = github_repository_ruleset.all_branches
      default_branch = github_repository_ruleset.default_branch
      checks         = github_repository_ruleset.checks
    } : name => length(rule.bypass_actors)
  }
}
