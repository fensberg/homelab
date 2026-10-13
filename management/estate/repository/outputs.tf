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

output "the_estates_credential_is_handed_to" {
  description = "The only branch a job may be handed the estate's credential on, and whether it is held to it: for the root's tests."
  value = {
    branch         = github_repository_environment_deployment_policy.estate.branch_pattern
    environment    = github_repository_environment_deployment_policy.estate.environment
    held_to_it     = one(github_repository_environment.estate.deployment_branch_policy).custom_branch_policies && !one(github_repository_environment.estate.deployment_branch_policy).protected_branches
    admins_pass_by = github_repository_environment.estate.can_admins_bypass
  }
}

output "pull_request_runners_site" {
  description = "The site a pull request's jobs are sent to, as workflows read it: for the root's tests."
  value       = github_actions_variable.pull_request_runners_site.value
}

output "rules" {
  description = "Who may pass each rule on the repository's branches and tags by, as a count per rule: for the root's tests."
  value = {
    for name, rule in {
      releases       = github_repository_ruleset.releases
      all_branches   = github_repository_ruleset.all_branches
      default_branch = github_repository_ruleset.default_branch
      checks         = github_repository_ruleset.checks
      review         = github_repository_ruleset.review
    } : name => length(rule.bypass_actors)
  }
}
