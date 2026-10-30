# A fact is written once, in its file under config/facts, and every program
# reads it from there. OpenTofu reads it with jsondecode(file(...)), so in a
# plan a value read from a fact is a reference and never a constant; a
# constant equal to one is the same value written out again, which the next
# change to the fact leaves behind.
#
# WHAT A PLAN CAN SHOW. A plan's configuration gives an argument written
# wholly as a literal as its constant_value, and keeps no locals at all. So
# this sees a fact restated in a resource's own arguments, in any module, and
# not one restated as a local; what it cannot see is held where the value is
# written, as policy/conftest/rules/dns_resolvers does for the resolvers.
package rules.plan_restated_facts

import data.lib
import rego.v1

# Every string a fact holds, with the fact it is from: its comments, under a
# name beginning with an underscore, are not facts, and an empty string is no
# value to restate.
held contains [name, v] if {
	some name, fact in lib.facts
	walk(fact, [path, v])
	count(path) > 0
	not startswith(path[0], "_")
	is_string(v)
	v != ""
}

# Every string written as a constant in a plan's configuration, whole or as
# a member of one.
constants contains [path, address, v] if {
	some [path, address, r] in lib.plan_configuration
	walk(r, [_, node])
	is_object(node)
	walk(node.constant_value, [_, v])
	is_string(v)
}

deny contains msg if {
	some [path, address, v] in constants
	some [name, held_value] in held
	held_value == v
	msg := $"{address} writes \"{v}\" as a constant ({path}), and that is config/facts/{name}.json's to say. Read it from the fact, so that changing the fact changes this too"
}

deny contains msg if {
	count(lib.facts) == 0
	msg := "no fact was read from config/facts, so nothing was held to being written once"
}

deny contains msg if {
	count(constants) == 0
	msg := "no plan's configuration writes any constant, so the plans were not read"
}
