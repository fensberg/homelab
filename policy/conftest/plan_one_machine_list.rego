# Every kind of machine is configured from the one list of what a machine is
# given.
#
# WHY. A zone's machine was configured from a list of its own, written beside
# the one the others use. What a machine needs was then added to that one - a
# hostname, a place for volumes, the labels that say where it is - and never
# to the zone's, because nothing said there were two. The first zone's
# machine built would have joined nameless and with nowhere to keep
# anything. One list cannot part from itself.
#
# EVERY KIND, wherever it is declared: each configuration the operating
# system's provider is asked to generate, read from the plan's configuration,
# and not the kinds there were when this was written.
package rules.plan_one_machine_list

import data.lib
import rego.v1

the_list := "local.machine_patches"

kinds contains [address, r] if {
	some [_, address, r] in lib.plan_configuration
	r.mode == "data"
	r.type == "talos_machine_configuration"
}

deny contains msg if {
	count(kinds) == 0
	msg := "no talos_machine_configuration is in any plan's configuration, so this checked nothing"
}

deny contains msg if {
	some [address, r] in kinds
	not the_list in object.get(object.get(r.expressions, "config_patches", {}), "references", [])
	msg := sprintf("%s is not configured from %s. A kind of machine with a list of its own is given whatever that list had the day it was written, and none of what every machine has been given since.", [address, the_list])
}
