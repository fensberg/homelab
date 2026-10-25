# A checkout may only be told how much history to fetch and not to keep the
# token. Anything that changes WHAT is checked out lets a caller place code
# where later steps execute, which in a run holding secrets is how the clerk
# handed a stranger's pull request its App's key (#409).
package rules.checkout_settings

import data.lib
import rego.v1

checkouts contains [file, step] if {
	some file, doc in lib.runnable
	some step in all_steps(doc)
	contains(object.get(step, "uses", ""), "actions/checkout@")
}

all_steps(doc) := array.concat(
	[s | some job in object.get(doc, "jobs", {}); some s in object.get(job, "steps", [])],
	object.get(object.get(doc, "runs", {}), "steps", []),
)

deny contains msg if {
	count(checkouts) == 0
	msg := "no actions/checkout step was found in any workflow or action, so the checkout rule checked nothing"
}

deny contains msg if {
	some [file, step] in checkouts
	some key, _ in object.get(step, "with", {})
	not key in {"fetch-depth", "persist-credentials"}
	msg := $"{file} gives actions/checkout \"{key}\". A checkout here may only be told how deep to fetch. Anything that changes WHAT is checked out lets a caller place code that was never reviewed where later steps execute (#409). To read other code, fetch it as data into its own worktree and do not run it, as clerk.yml does."
}
