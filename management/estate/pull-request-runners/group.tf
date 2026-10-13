resource "github_actions_runner_group" "this" {
  name                       = "pull-requests"
  visibility                 = "selected"
  selected_repository_ids    = var.repositories
  allows_public_repositories = true
  restricted_to_workflows    = false
}
