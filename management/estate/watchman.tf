# The watchman: the party outside every site that says when one has gone
# quiet. One for the estate, because it has to outlive whatever it watches.
# What it does is in watchman/watchman.mjs and what it is given in
# watchman/watchman.tf.
module "watchman" {
  source = "./watchman"

  account_id        = local.access.account_id
  sites             = toset(keys(local.sites))
  workers_subdomain = local.access.workers_subdomain
  channel           = local.config.alerting.webhook_url
}
