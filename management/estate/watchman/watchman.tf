# The watchman: the party outside every site that says when one has gone
# quiet. What it does and why is at the top of watchman.mjs; this is
# where it is given what it needs and nothing else.
#
# It is the estate's and not a site's because it has to outlive whatever it
# watches: a watchman in a site's state is destroyed with the site, and one
# running in a site stops with it.
#
# BUILT TO STAY ON THE FREE PLAN. An account that goes over a free limit has
# its requests refused, not billed, and these are the limits it lives inside:
#
#   five timers an account        this is one
#   100,000 requests a day        a ring from each site every ten minutes is 144
#   1,000 writes a day            the same 144 for each site, and one more
#                                 each time a site changes between quiet and
#                                 heard
#
# The operator's bound is 200 writes a day for a site, which is why a ring is
# every ten minutes and not every five.

locals {
  # How often the watchman looks round, and how long a silence is before it
  # says so: two rings missed, and room for one that is merely late.
  looks_every_minutes = 10
  quiet_after_minutes = 25

  source = "${path.module}/watchman.mjs"

  # What it uses of the free plan in a day, which the estate adds up. A site rings as
  # often as the watchman looks round. A ring is a request and a write; a
  # look round reads two values for each site; and ten more writes a day for
  # each site is room for it to change between quiet and heard five times.
  looks_a_day = 24 * 60 / local.looks_every_minutes
  use = {
    timers         = length(cloudflare_workers_cron_trigger.watchman.schedules)
    requests_a_day = length(var.sites) * local.looks_a_day
    reads_a_day    = length(var.sites) * local.looks_a_day * 2
    writes_a_day   = length(var.sites) * (local.looks_a_day + 10)
  }
}

# What each site rings with. Generated here because it ends up in state
# whoever makes it, and a secret that is in state is the estate's to make.
# Letters and digits only: it travels in a header.
resource "random_password" "ring" {
  for_each = var.sites

  length  = 48
  special = false
}

# Where the watchman writes down when it last heard from each site.
resource "cloudflare_workers_kv_namespace" "heard" {
  account_id = var.account_id
  title      = "watchman"
}

resource "cloudflare_workers_script" "watchman" {
  account_id  = var.account_id
  script_name = "watchman"

  main_module    = "watchman.mjs"
  content_file   = local.source
  content_sha256 = filesha256(local.source)

  compatibility_date = "2026-10-01"

  bindings = [
    {
      type         = "kv_namespace"
      name         = "HEARD"
      namespace_id = cloudflare_workers_kv_namespace.heard.id
    },
    {
      # Each site's key and the SHA-256 of its secret. The watchman is never
      # given a secret itself, so nothing read out of its settings can ring
      # for a site that has stopped.
      type = "plain_text"
      name = "SITES"
      text = jsonencode({ for k, p in random_password.ring : k => sha256(p.result) })
    },
    {
      type = "plain_text"
      name = "QUIET_AFTER_MINUTES"
      text = tostring(local.quiet_after_minutes)
    },
    {
      # The channel every site's alerts already reach, so a site going quiet
      # is said where its alerts would have been, by the same hand.
      type = "secret_text"
      name = "SLACK_WEBHOOK"
      text = var.channel
    },
  ]
}

# Reachable at the account's own address for Workers, which needs no zone and
# no route. Previews are other addresses for the same code, and off.
resource "cloudflare_workers_script_subdomain" "watchman" {
  account_id       = var.account_id
  script_name      = cloudflare_workers_script.watchman.script_name
  enabled          = true
  previews_enabled = false
}

resource "cloudflare_workers_cron_trigger" "watchman" {
  account_id  = var.account_id
  script_name = cloudflare_workers_script.watchman.script_name
  schedules   = [{ cron = "*/${local.looks_every_minutes} * * * *" }]
}

# What a site is granted so that it can ring: where, and with what.
output "heartbeat" {
  sensitive = true
  value = {
    for k, p in random_password.ring : k => {
      provider = "cloudflare"
      url      = "https://${cloudflare_workers_script.watchman.script_name}.${var.workers_subdomain}.workers.dev/${k}"
      secret   = p.result
    }
  }
}

output "use" {
  description = "What the watchman uses of the free plan in a day."
  value       = local.use
}

output "timings" {
  description = "How often it looks round and how long a silence is before it says so, in minutes."
  value       = { looks_every = local.looks_every_minutes, quiet_after = local.quiet_after_minutes }
}

# What it is given that is not a secret, by name: read by the estate's tests
# to hold that no site's secret is among it.
output "given_in_the_clear" {
  value = { for b in cloudflare_workers_script.watchman.bindings : b.name => b.text if b.type == "plain_text" }
}
