# The label mechanism is gone. Two mechanisms for one acknowledgement is two
# things to keep in step, and the weaker one becomes the one people use.
#
# It is one property of the sensitive-path gate; the gate is described in
# rules.sensitive_gate_conversation.
package rules.sensitive_gate_labels

import data.lib
import rego.v1

workflow := ".github/workflows/sensitive-paths.yml"

doc := lib.workflows[workflow]

dead := ["removeLabel", "addLabels", "--json labels", "labeled"]

deny contains msg if {
	not lib.workflows[workflow]
	msg := $"{workflow} is not there, so whether it still uses labels cannot be read"
}

deny contains msg if {
	walk(doc, [path, v])
	some s in array.concat([v | is_string(v)], [k | some k in path; is_string(k)])
	some word in dead
	contains(s, word)
	msg := $"the workflow still manipulates or reads labels (\"{word}\"). The acknowledgement is a conversation now; a label alongside it is a second, weaker gate that will drift from this one."
}
