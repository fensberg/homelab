# Every Worker the estate declares is counted against the free plan.
#
# What runs at the vendor for nothing is held to what the vendor gives for
# nothing: each Worker's module says what it uses in a day, the uses are
# added up, and a plan that asks for more is refused before anything changes
# (the `free_plan_use` output, proved by the estate root's own tests). That
# sum is only the truth while everything is in it. A Worker declared and left
# out uses its share of the day's requests and writes all the same, and the
# first anybody knows is the vendor refusing the rest - silently, part way
# through a day, for everything on the account.
#
# ON WHAT TOFU WOULD MAKE. A Worker is found in the estate's plan, wherever
# in its modules it is declared. The sum is the module the free_plan_use
# output reads, and what that module is given to add up is in the plan as
# the references of its argument: a plan keeps no locals, which is why the
# sum is a module.
package rules.plan_free_plan

import data.lib
import rego.v1

# Each module of the estate's root that declares a Worker, by its call's name:
# "" for a Worker in the root itself.
workers contains [address, call] if {
	some [path, address, r] in lib.plan_configuration
	lib.plan_scope(path) == "estate"
	r.type == "cloudflare_workers_script"
	r.mode == "managed"
	parts := split(address, ".")
	call := root_call(parts)
}

root_call(parts) := parts[1] if parts[0] == "module"

root_call(parts) := "" if parts[0] != "module"

# The module the free_plan_use output reads, and what it is given to add up.
summed_by contains call if {
	some [path, "free_plan_use", output] in lib.plan_outputs
	lib.plan_scope(path) == "estate"
	some ref in output.expression.references
	parts := split(ref, ".")
	parts[0] == "module"
	call := parts[1]
}

counted contains counted_call if {
	some [path, call, module] in lib.plan_module_calls
	lib.plan_scope(path) == "estate"
	call in summed_by
	some ref in module.expressions.uses.references
	parts := split(ref, ".")
	parts[0] == "module"
	counted_call := parts[1]
}

deny contains msg if {
	some [address, call] in workers
	call != ""
	not call in counted
	msg := $"{address} is a Worker, and module.{call}'s use is not among what the free_plan_use output adds up. Give the module a use output - timers, requests, reads and writes a day - and name it among the uses in free-plan.tf. Uncounted, it uses its share all the same, and what goes over is refused at the vendor for everything on the account."
}

deny contains msg if {
	some [address, ""] in workers
	msg := $"{address} is a Worker declared in the estate's root, where nothing can say what it uses. Declare it in a module that gives its use as an output, and name that among the uses in free-plan.tf."
}

deny contains msg if {
	count(workers) == 0
	msg := "no Worker is in the estate's plan, so nothing was checked: the reading has stopped matching, or the sum is of nothing"
}

deny contains msg if {
	count(summed_by) == 0
	msg := "the estate's free_plan_use output reads no module, so what is added up against the free plan cannot be read"
}
