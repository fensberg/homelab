# Every Worker the estate declares is counted against the free plan.
#
# What runs at the vendor for nothing is held to what the vendor gives for
# nothing: each Worker says what it uses in a day, the uses are added up, and
# a plan that asks for more is refused before anything changes (the
# `free_plan_use` output, proved by the estate root's own tests). That sum is
# only the truth while everything is in it. A Worker declared and left out
# uses its share of the day's requests and writes all the same, and the first
# anybody knows is the vendor refusing the rest - silently, part way through a
# day, for everything on the account.
#
# So a Worker is found by what it is, wherever it is declared, and must be
# named in the map the sum is made from.
package rules.free_plan

import rego.v1

tf contains f if {
	some f in input
	endswith(f.path, ".tf")
}

workers contains [f.path, name] if {
	some f in tf
	some name, _ in object.get(object.get(f.contents, "resource", {}), "cloudflare_workers_script", {})
}

counted contains name if {
	some f in tf
	some block in object.get(f.contents, "locals", [])
	some name, _ in object.get(block, "workers_use", {})
}

deny contains msg if {
	some [path, name] in workers
	not name in counted
	msg := sprintf("%s declares the Worker %q, and it is not counted against the free plan. Say what it uses in a day - timers, requests, reads and writes - and name it in workers_use. Uncounted, it uses its share all the same, and what goes over is refused at the vendor for everything on the account.", [path, name])
}

deny contains msg if {
	count(workers) == 0
	msg := "found no Worker declared anywhere, so nothing was checked: the reader has stopped matching, or the sum is of nothing"
}
