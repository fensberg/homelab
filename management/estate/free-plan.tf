# What the estate runs at Cloudflare for nothing, held to what Cloudflare
# gives for nothing.
#
# An account on the free plan that goes over a limit has its requests
# refused, not billed - which keeps the bill at nothing and stops whatever
# was running, silently, part way through a day. For the watchman that is the
# one failure nobody is told about. So everything here that runs on the free
# plan says what it uses in a day, the uses are added up, and a plan that
# asks for more than the free plan gives is refused before anything changes:
# a Worker added, a timer added, or a site too many.
#
# tests/go/repo refuses a Worker that is declared and not counted here.

variable "free_plan" {
  type = object({
    timers         = number
    requests_a_day = number
    reads_a_day    = number
    writes_a_day   = number
  })
  default = {
    timers         = 5
    requests_a_day = 100000
    reads_a_day    = 100000
    writes_a_day   = 1000
  }
  description = <<-EOT
    The free plan's limits for an account, from Cloudflare's own pages for
    Workers and for its key-value store, read 2026-10-08. A variable so that a
    test can plan against a smaller plan; nothing that builds the estate sets
    it, and raising it here does not raise it at Cloudflare.
  EOT
}

locals {
  # Every Worker the estate runs, and what it uses in a day.
  workers_use = {
    watchman = module.watchman.use
  }

  free_plan_use = {
    for what, most in var.free_plan : what => sum([for use in values(local.workers_use) : use[what]])
  }
  over_the_free_plan = [
    for what, most in var.free_plan : "${what}: ${local.free_plan_use[what]} against ${most}"
    if local.free_plan_use[what] > most
  ]
}

output "free_plan_use" {
  description = "What the estate uses of the free plan in a day, by limit."
  value       = local.free_plan_use

  precondition {
    condition     = length(local.over_the_free_plan) == 0
    error_message = "The estate would use more than Cloudflare's free plan gives, and what is over is refused there, not billed: ${join("; ", local.over_the_free_plan)}. Do less often what is done, or take the decision to pay for a plan - which is the operator's, and is not made by raising the figure here."
  }
}
