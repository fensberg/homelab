# Only the repositories given, and of those only the workflows given, each
# as it is on the branch given. A workflow changed on any other branch, or
# started by hand from one, is refused a runner by the platform, whatever
# its file asks for.
#
# The repository is public, so the group has to admit a public repository to
# admit it at all. That is why the workflows are held to a branch: nothing
# reaches that branch unreviewed.
resource "github_actions_runner_group" "this" {
  name                       = var.name
  visibility                 = "selected"
  selected_repository_ids    = var.repositories
  allows_public_repositories = true
  restricted_to_workflows    = true
  selected_workflows         = var.workflows
}
