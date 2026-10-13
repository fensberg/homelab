# What a workflow reads that is the estate's to say and not a secret.
#
# Which site hosts the pools a pull request's jobs run in. A job on the
# estate's runners names the site it runs for, and a pull request's job has
# no site of its own: it is the repository's work and runs wherever the
# pools are. So the estate says where, here, and a job asks for
# `pr-<pool>-${{ vars.pull_request_runners_site }}`. Moving the pools to
# another site is this value.
resource "github_actions_variable" "pull_request_runners_site" {
  repository    = var.name
  variable_name = "PULL_REQUEST_RUNNERS_SITE"
  value         = var.pull_request_runners_site
}
