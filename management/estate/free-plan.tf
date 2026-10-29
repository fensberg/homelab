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
# policy/conftest/rules/plan_free_plan refuses a Worker that is declared and
# not counted here.

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

# Every Worker the estate runs, and what it uses in a day, added up. A
# Worker's module gives its use as an output, and it is named here.
module "free_plan" {
  source = "./free-plan"

  limits = var.free_plan
  uses = {
    watchman = module.watchman.use
  }
}

output "free_plan_use" {
  description = "What the estate uses of the free plan in a day, by limit."
  value       = module.free_plan.use

  precondition {
    condition     = length(module.free_plan.over) == 0
    error_message = "The estate would use more than Cloudflare's free plan gives, and what is over is refused there, not billed: ${join("; ", module.free_plan.over)}. Do less often what is done, or take the decision to pay for a plan - which is the operator's, and is not made by raising the figure here."
  }
}
