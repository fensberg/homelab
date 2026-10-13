output "admits" {
  description = "Whom the group admits, for the root's tests."
  value = {
    visibility   = github_actions_runner_group.this.visibility
    repositories = github_actions_runner_group.this.selected_repository_ids
    restricted   = github_actions_runner_group.this.restricted_to_workflows
    workflows    = github_actions_runner_group.this.selected_workflows
  }
}
