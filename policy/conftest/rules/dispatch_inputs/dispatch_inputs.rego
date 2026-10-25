# A workflow that takes inputs when dispatched can publish nothing: whoever
# dispatches it would choose what goes into what it publishes.
package rules.dispatch_inputs

import data.lib
import rego.v1

dispatched(doc) := d if {
	on := object.get(doc, "on", object.get(doc, "true", {}))
	is_object(on)
	d := on.workflow_dispatch
	is_object(d)
	d.inputs
}

writes(null) := {"declares no permissions, so it has whatever the repository's default is"}

writes(perms) := set() if perms in {"read-all", {}}

writes(perms) := {$"has permissions: {perms}"} if {
	is_string(perms)
	perms != "read-all"
}

writes(perms) := {$"may {level} {scope}" | some scope, level in perms; not level in {"read", "none"}} if {
	is_object(perms)
	perms != {}
}

deny contains msg if {
	some file, doc in lib.workflows
	dispatched(doc)
	some why in writes(object.get(doc, "permissions", null))
	msg := $"{file} takes inputs and {why}. Whoever dispatches it would choose what goes into what it publishes. Take the input away, or the write."
}

deny contains msg if {
	some file, doc in lib.workflows
	dispatched(doc)
	some name, job in object.get(doc, "jobs", {})
	job.permissions
	some why in writes(job.permissions)
	msg := $"{file} takes inputs and its job {name} {why}. Whoever dispatches it would choose what goes into what it publishes. Take the input away, or the write."
}
