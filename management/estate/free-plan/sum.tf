# The free plan's limits, and what everything given here uses of them in a
# day, added up.
#
# A module, and not locals in the root, so that what is added up is seen in
# the estate's plan: a plan keeps no locals, and keeps the references of what
# a module is given. policy/conftest/rules/plan_free_plan reads them, and
# refuses a Worker whose use is not among them.
variable "uses" {
  type        = map(map(number))
  description = "What each thing that runs on the free plan uses of it in a day, by limit."
}

variable "limits" {
  type        = map(number)
  description = "The free plan's limits for the account, by the same names."
}

locals {
  use = {
    for what, most in var.limits : what => sum(concat([0], [for u in values(var.uses) : lookup(u, what, 0)]))
  }
}

output "use" {
  description = "What everything given uses of the free plan in a day, by limit."
  value       = local.use
}

output "over" {
  description = "Each limit what is given would go over, with what it would use."
  value = [
    for what, most in var.limits : "${what}: ${local.use[what]} against ${most}"
    if local.use[what] > most
  ]
}
